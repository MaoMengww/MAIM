package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Public HTTP shapes only; exact decimal positions must never pass through float64.
type inboxChange struct {
	Position       decimal       `json:"position"`
	ConversationID decimal       `json:"conversation_id"`
	Kind           string        `json:"kind"`
	Message        storedMessage `json:"message"`
}

type inboxSnapshot struct {
	Conversation struct {
		conversationView
		IsMuted  bool `json:"is_muted"`
		IsPinned bool `json:"is_pinned"`
	} `json:"conversation"`
	Messages []storedMessage `json:"messages"`
}

type inboxSyncResult struct {
	Changes         []inboxChange   `json:"changes"`
	HasMore         bool            `json:"has_more"`
	NextPosition    decimal         `json:"next_position"`
	RebuildRequired bool            `json:"rebuild_required"`
	Conversations   []inboxSnapshot `json:"conversations"`
	RebuildReason   string          `json:"rebuild_reason"`
}

func (d *driver) inboxRead(step string, caller account, position decimal, limit int) (inboxSyncResult, error) {
	var result inboxSyncResult
	path := fmt.Sprintf("/messages/sync?position=%s&limit=%d", position, limit)
	if err := d.request(http.MethodGet, path, caller.token, nil, &result); err != nil {
		return result, fmt.Errorf("user-sync.%s: %w", step, err)
	}
	if result.NextPosition <= 0 {
		return result, fmt.Errorf("user-sync.%s: next_position 必须为可续用的正位点", step)
	}
	if result.RebuildRequired {
		if result.HasMore || len(result.Changes) != 0 || (result.RebuildReason != "new_device" && result.RebuildReason != "unknown_position" && result.RebuildReason != "expired_position") {
			return result, fmt.Errorf("user-sync.%s: 重建必须与增量显式区分并返回合法原因", step)
		}
		return result, nil
	}
	if position == 0 || result.RebuildReason != "" || len(result.Conversations) != 0 || result.NextPosition < position || len(result.Changes) > limit {
		return result, fmt.Errorf("user-sync.%s: 增量/重建类型、页上限或位点不符合契约", step)
	}
	previous := position
	for _, change := range result.Changes {
		if change.Position <= previous || change.ConversationID <= 0 || change.Message.ConvID != change.ConversationID || change.Message.MessageID <= 0 || change.Kind == "" {
			return result, fmt.Errorf("user-sync.%s: 变化位点必须递增且包含匹配会话的完整消息", step)
		}
		previous = change.Position
	}
	if result.NextPosition < previous {
		return result, fmt.Errorf("user-sync.%s: next_position=%s 不能落后于本页末个可见变化=%s", step, result.NextPosition, previous)
	}
	if result.HasMore && result.NextPosition <= position {
		return result, fmt.Errorf("user-sync.%s: has_more 页未推进位点", step)
	}
	return result, nil
}

func checkInboxRebuild(step, reason string, result inboxSyncResult) error {
	if !result.RebuildRequired || result.RebuildReason != reason {
		return fmt.Errorf("user-sync.%s: 期望显式 %s 重建，实际 rebuild_required=%t reason=%q", step, reason, result.RebuildRequired, result.RebuildReason)
	}
	return nil
}

// inboxDrain keeps one user position across every conversation. Empty tail pages
// are retried only while confirmed Kafka-backed messages remain outstanding.
func (d *driver) inboxDrain(step string, caller account, position decimal, expected []sentMessage, requirePagination bool) (decimal, error) {
	pending := make(map[decimal]sentMessage, len(expected))
	for _, message := range expected {
		pending[message.MessageID] = message
	}
	seen := make(map[decimal]bool, len(expected))
	deadline := time.Now().Add(d.timeout)
	paginated := false
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return position, fmt.Errorf("user-sync.%s: 截止时间内缺失 %d 条已确认消息", step, len(pending))
		}
		bounded := *d
		bounded.timeout = remaining
		page, err := bounded.inboxRead(step, caller, position, 1)
		if err != nil {
			return position, err
		}
		if page.RebuildRequired {
			return position, fmt.Errorf("user-sync.%s: 已知有效位点 %s 不应触发重建", step, position)
		}
		// This fixture has no filtered references: limit=1 must stop at the
		// returned entry, rather than skipping any remaining visible messages.
		if len(page.Changes) > 0 && page.NextPosition != page.Changes[len(page.Changes)-1].Position {
			return position, fmt.Errorf("user-sync.%s: 无过滤fixture的next_position必须等于本页末变化", step)
		}
		for _, change := range page.Changes {
			message, ok := pending[change.Message.MessageID]
			if !ok || seen[change.Message.MessageID] || change.Kind != "message.new" {
				return position, fmt.Errorf("user-sync.%s: 收到非本用户预期消息、重复消息或错误 kind（message_id=%s conv_id=%s）", step, change.Message.MessageID, change.ConversationID)
			}
			if err := checkStoredMessage("user-sync."+step, change.Message, message); err != nil {
				return position, err
			}
			seen[message.MessageID] = true
			delete(pending, message.MessageID)
		}
		position = page.NextPosition
		paginated = paginated || page.HasMore
		if page.HasMore {
			continue
		}
		if len(pending) == 0 {
			if requirePagination && !paginated {
				return position, fmt.Errorf("user-sync.%s: 多条积压 limit=1 未实际观察到 has_more=true", step)
			}
			return position, nil
		}
		time.Sleep(min(100*time.Millisecond, max(0, time.Until(deadline))))
	}
}

func checkInboxSnapshots(step string, result inboxSyncResult, expected map[decimal][]sentMessage, groupID decimal, groupName string) error {
	if len(result.Conversations) != len(expected) {
		return fmt.Errorf("user-sync.%s: 完整会话列表期望 %d 项，实际 %d 项（不得泄露其他用户会话）", step, len(expected), len(result.Conversations))
	}
	seen := make(map[decimal]bool, len(expected))
	for _, snapshot := range result.Conversations {
		conv := snapshot.Conversation
		messages, ok := expected[conv.ID]
		if !ok || seen[conv.ID] || len(snapshot.Messages) != len(messages) {
			return fmt.Errorf("user-sync.%s: 非预期/重复会话或最近历史条数不匹配 conv_id=%s", step, conv.ID)
		}
		seen[conv.ID] = true
		if conv.ID == groupID {
			if conv.Type != 2 || conv.Name != groupName || !conv.IsMuted || !conv.IsPinned {
				return fmt.Errorf("user-sync.%s: 群会话信息或置顶/免打扰设置未保留", step)
			}
		} else if conv.Type != 1 || conv.IsMuted || conv.IsPinned {
			return fmt.Errorf("user-sync.%s: 单聊类型或用户私有设置串到其他会话", step)
		}
		for i, message := range snapshot.Messages {
			if err := checkStoredMessage("user-sync."+step, message, messages[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *driver) userSync() error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	sender, err := d.register("sync_sender", suffix)
	if err != nil {
		return err
	}
	receiver, err := d.register("sync_receiver", suffix)
	if err != nil {
		return err
	}
	if sender.id == receiver.id {
		return errors.New("user-sync.identity: 两个账号必须有不同 user_id")
	}
	initial, err := d.inboxRead("empty-new-device", receiver, 0, 1)
	if err != nil {
		return err
	}
	if err := checkInboxRebuild("empty-new-device", "new_device", initial); err != nil {
		return err
	}
	if len(initial.Conversations) != 0 {
		return errors.New("user-sync.empty-new-device: 全新用户不应有会话或其他用户历史")
	}
	if _, err := d.inboxDrain("empty-resume", receiver, initial.NextPosition, nil, false); err != nil {
		return err
	}
	for _, query := range []string{"position=garbage&limit=1", "position=9223372036854775808&limit=1", "position=0&limit=-1", "position=0&limit=2147483648"} {
		err := d.request(http.MethodGet, "/messages/sync?"+query, receiver.token, nil, nil)
		var rejected *apiError
		if !errors.As(err, &rejected) || rejected.status != http.StatusBadRequest || rejected.code == 0 {
			return fmt.Errorf("user-sync.invalid-query: %s 期望HTTP400参数拒绝，实际 %v", query, err)
		}
	}
	negative, err := d.inboxRead("negative-position", receiver, -1, 1)
	if err != nil {
		return err
	}
	if err := checkInboxRebuild("negative-position", "unknown_position", negative); err != nil {
		return err
	}
	senderInitial, err := d.inboxRead("sender-initial", sender, 0, 1)
	if err != nil {
		return err
	}
	if err := checkInboxRebuild("sender-initial", "new_device", senderInitial); err != nil {
		return err
	}
	create := func(step string, input map[string]any) (decimal, error) {
		var result struct {
			ID decimal `json:"conversation_id"`
		}
		if err := d.request(http.MethodPost, "/convs", sender.token, input, &result); err != nil {
			return 0, fmt.Errorf("user-sync.%s: %w", step, err)
		}
		if result.ID <= 0 {
			return 0, fmt.Errorf("user-sync.%s: 没有有效 conversation_id", step)
		}
		return result.ID, nil
	}
	singleID, err := create("create-single", map[string]any{"type": "single", "peer_user_id": receiver.id.String()})
	if err != nil {
		return err
	}
	groupName := "e2e_sync_group_" + suffix
	groupID, err := create("create-group", map[string]any{"type": "group", "group_name": groupName, "member_ids": []string{receiver.id.String()}})
	if err != nil {
		return err
	}
	// A sender-only group gives isolation a real negative fixture without a DB seam.
	privateID, err := create("create-sender-only-group", map[string]any{"type": "group", "group_name": "e2e_sync_private_" + suffix})
	if err != nil {
		return err
	}
	if singleID == groupID || privateID == singleID || privateID == groupID {
		return errors.New("user-sync.conversations: 不同会话返回相同 ID")
	}
	settingsPath := "/convs/" + groupID.String() + "/settings"
	if err := d.request(http.MethodPut, settingsPath, receiver.token, map[string]bool{"is_muted": true, "is_pinned": true}, nil); err != nil {
		return fmt.Errorf("user-sync.settings: %w", err)
	}
	var shared []sentMessage
	history := map[decimal][]sentMessage{singleID: nil, groupID: nil}
	for i := range 3 {
		for _, convID := range []decimal{singleID, groupID} {
			previous := decimal(0)
			if messages := history[convID]; len(messages) > 0 {
				previous = messages[len(messages)-1].Seq
			}
			message, err := d.sendMessage(sender, convID, fmt.Sprintf("%s_sync_%s_%d", suffix, convID, i), previous)
			if err != nil {
				return err
			}
			shared = append(shared, message)
			history[convID] = append(history[convID], message)
		}
	}
	private, err := d.sendMessage(sender, privateID, suffix+"_sender_only", 0)
	if err != nil {
		return err
	}
	// Observe publication through another user's own sync, never Kafka/SQL internals.
	if _, err := d.inboxDrain("sender-published", sender, senderInitial.NextPosition, append(shared, private), false); err != nil {
		return err
	}
	position, err := d.inboxDrain("receiver-cross-conversation-pages", receiver, initial.NextPosition, shared, true)
	if err != nil {
		return err
	}
	if _, err := d.inboxDrain("receiver-caught-up", receiver, position, nil, false); err != nil {
		return err
	}
	rebuild, err := d.inboxRead("new-device-rebuild", receiver, 0, 2)
	if err != nil {
		return err
	}
	if err := checkInboxRebuild("new-device-rebuild", "new_device", rebuild); err != nil {
		return err
	}
	latest := map[decimal][]sentMessage{singleID: history[singleID][1:], groupID: history[groupID][1:]}
	if err := checkInboxSnapshots("new-device-rebuild", rebuild, latest, groupID, groupName); err != nil {
		return err
	}
	continued, err := d.sendMessage(sender, groupID, suffix+"_after_rebuild", history[groupID][2].Seq)
	if err != nil {
		return err
	}
	if _, err := d.inboxDrain("rebuild-resume", receiver, rebuild.NextPosition, []sentMessage{continued}, false); err != nil {
		return err
	}
	unknown, err := d.inboxRead("unknown-position", receiver, decimal(1<<63-1), 2)
	if err != nil {
		return err
	}
	if err := checkInboxRebuild("unknown-position", "unknown_position", unknown); err != nil {
		return err
	}
	latest[groupID] = []sentMessage{history[groupID][2], continued}
	if err := checkInboxSnapshots("unknown-position", unknown, latest, groupID, groupName); err != nil {
		return err
	}
	_, err = d.inboxDrain("unknown-rebuild-resume", receiver, unknown.NextPosition, nil, false)
	return err
}
