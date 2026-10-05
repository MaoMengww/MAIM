package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Public HTTP shapes only; exact decimal positions must never pass through float64.
type inboxChange struct {
	Position       decimal            `json:"position"`
	ConversationID decimal            `json:"conversation_id"`
	Kind           string             `json:"kind"`
	Message        storedMessage      `json:"message"`
	MessageID      decimal            `json:"message_id"`
	Conversation   *inboxConversation `json:"conversation"`
	LastReadSeq    decimal            `json:"last_read_seq"`
}

type inboxConversation struct {
	conversationView
	IsMuted      bool   `json:"is_muted"`
	IsPinned     bool   `json:"is_pinned"`
	Avatar       string `json:"avatar"`
	Announcement string `json:"announcement"`
}

type inboxSnapshot struct {
	Conversation inboxConversation `json:"conversation"`
	Messages     []storedMessage   `json:"messages"`
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
		if change.Position <= previous || change.ConversationID <= 0 {
			return result, fmt.Errorf("user-sync.%s: 变化位点必须递增且包含会话", step)
		}
		switch change.Kind {
		case "message.new", "message.edited", "message.recalled":
			if change.Message.ConvID != change.ConversationID || change.Message.MessageID <= 0 {
				return result, fmt.Errorf("user-sync.%s: 消息变更缺少完整消息", step)
			}
		case "message.deleted":
			if change.MessageID <= 0 {
				return result, fmt.Errorf("user-sync.%s: 删除缺少message_id", step)
			}
		case "conversation.upsert", "read.updated":
			if change.Conversation == nil || change.Conversation.ID != change.ConversationID {
				return result, fmt.Errorf("user-sync.%s: 会话变更缺少完整会话", step)
			}
		case "conversation.removed":
		default:
			return result, fmt.Errorf("user-sync.%s: 未知变化kind=%s", step, change.Kind)
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
			if change.Kind == "conversation.upsert" {
				continue
			}
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

// inboxChanges exercises the public reconnect contract, not database internals.
func (d *driver) inboxChanges(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("changes_owner", suffix)
	if err != nil {
		return err
	}
	member, err := d.register("changes_member", suffix)
	if err != nil {
		return err
	}
	initial, err := d.inboxRead("changes-initial", member, 0, 50)
	if err != nil {
		return err
	}
	position := initial.NextPosition
	var created struct {
		ID decimal `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", owner.token, map[string]any{
		"type": "group", "group_name": "changes_" + suffix, "member_ids": []string{member.id.String()},
	}, &created); err != nil {
		return err
	}
	convID := created.ID
	path := "/convs/" + convID.String()
	collect := func(step string, matches func([]inboxChange) bool) ([]inboxChange, error) {
		var changes []inboxChange
		deadline := time.Now().Add(d.timeout)
		for time.Now().Before(deadline) {
			page, err := d.inboxRead(step, member, position, 50)
			if err != nil {
				return nil, err
			}
			if page.RebuildRequired {
				return nil, fmt.Errorf("%s: valid checkpoint rebuilt", step)
			}
			position = page.NextPosition
			changes = append(changes, page.Changes...)
			if !page.HasMore && matches(changes) {
				return changes, nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return nil, fmt.Errorf("%s: expected changes did not converge", step)
	}
	has := func(kind string, id decimal) func([]inboxChange) bool {
		return func(changes []inboxChange) bool {
			for _, change := range changes {
				if change.Kind == kind && change.ConversationID == convID && (id == 0 || change.Message.MessageID == id || change.MessageID == id) {
					return true
				}
			}
			return false
		}
	}
	if _, err := collect("offline-created", has("conversation.upsert", 0)); err != nil {
		return err
	}
	connOwner, err := d.connect(addressB, owner)
	if err != nil {
		return err
	}
	defer connOwner.Close()
	connMember, err := d.connect(addressA, member)
	if err != nil {
		return err
	}
	if err := d.ready(connMember); err != nil {
		connMember.Close()
		return err
	}
	connMember.Close() // Member A is offline while B sends and mutates messages.
	var sent []sentMessage
	seq := decimal(0)
	for i := range 3 {
		msg, err := d.sendMessage(owner, convID, fmt.Sprintf("%s_offline_%d", suffix, i), seq)
		if err != nil {
			return err
		}
		sent = append(sent, msg)
		seq = msg.Seq
	}
	if err := d.request(http.MethodPut, "/messages/"+sent[0].MessageID.String(), owner.token, map[string]any{"text": "edited_" + suffix}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, "/messages/"+sent[0].MessageID.String(), owner.token, map[string]any{"text": "edited_twice_" + suffix}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/messages/"+sent[1].MessageID.String()+"/recall", owner.token, map[string]any{}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, "/messages/"+sent[2].MessageID.String(), owner.token, map[string]any{"delete_for_all": true}, nil); err != nil {
		return err
	}
	offlinePosition := position
	connMember, err = d.connect(addressA, member)
	if err != nil {
		return err
	}
	defer connMember.Close()
	if err := d.ready(connMember); err != nil {
		return err
	}
	changes, err := collect("offline-message-mutations", func(changes []inboxChange) bool {
		return has("message.edited", sent[0].MessageID)(changes) && has("message.recalled", sent[1].MessageID)(changes) && has("message.deleted", sent[2].MessageID)(changes)
	})
	if err != nil {
		return err
	}
	edits := 0
	for _, change := range changes {
		if change.Kind == "message.edited" && change.Message.MessageID == sent[0].MessageID {
			edits++
			if change.Message.Text.Text != "edited_twice_"+suffix || change.Message.Type != 1 || change.Message.SenderID != owner.id || change.Message.EditCount != 2 {
				return errors.New("offline edit lost full message fields or latest body")
			}
		}
		if change.Kind == "message.recalled" && change.Message.Status != 2 {
			return errors.New("offline recall did not replay recalled status")
		}
	}
	if edits != 2 {
		return fmt.Errorf("two edits must have distinct replay entries, got %d", edits)
	}
	replay, err := d.inboxRead("mutation-replay-idempotent", member, offlinePosition, 50)
	if err != nil {
		return err
	}
	if replay.NextPosition != position || len(replay.Changes) != len(changes) {
		return errors.New("re-reading changes altered the user stream")
	}
	// Online mutation emits a wakeup; its authoritative body is the same sync page.
	if err := d.request(http.MethodPut, "/messages/"+sent[0].MessageID.String(), owner.token, map[string]any{"text": "online_" + suffix}, nil); err != nil {
		return err
	}
	if err := connMember.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return err
	}
	for {
		evt, err := readEvent(connMember)
		if err != nil {
			return err
		}
		if evt.Type == "inbox.changed" {
			break
		}
	}
	if _, err := collect("online-edit", func(changes []inboxChange) bool {
		for _, change := range changes {
			if change.Kind == "message.edited" && change.Message.Text.Text == "online_"+suffix {
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	connMember.Close()
	if err := d.request(http.MethodPut, path+"/info", owner.token, map[string]any{"name": "renamed_" + suffix, "avatar": "https://example.org/avatar.png"}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path+"/announcement", owner.token, map[string]any{"content": "notice_" + suffix}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path+"/settings", member.token, map[string]any{"is_muted": true, "is_pinned": true}, nil); err != nil {
		return err
	}
	if _, err := collect("offline-metadata-settings", func(changes []inboxChange) bool {
		for _, change := range changes {
			if change.Kind == "conversation.upsert" && change.Conversation != nil && change.Conversation.Name == "renamed_"+suffix && change.Conversation.Announcement == "notice_"+suffix && change.Conversation.Avatar == "https://example.org/avatar.png" && change.Conversation.IsMuted && change.Conversation.IsPinned {
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	snapshot, err := d.inboxRead("metadata-current-state", member, 0, 50)
	if err != nil {
		return err
	}
	if len(snapshot.Conversations) != 1 || !snapshot.Conversations[0].Conversation.IsMuted || !snapshot.Conversations[0].Conversation.IsPinned {
		return errors.New("settings did not converge in rebuild")
	}
	// One conversation keeps only the latest own-read entry, including stale input.
	readPosition := position
	for _, read := range []decimal{1, 2, 1, 999} {
		if err := d.request(http.MethodPut, path+"/read", member.token, map[string]any{"seq": read}, nil); err != nil {
			return err
		}
	}
	// A later own-message is an observable fence after all read events; it does
	// not add unread messages, and leaves the persisted read position unchanged.
	var readState struct {
		Conversation conversationView `json:"conversation"`
	}
	if err := d.request(http.MethodGet, path, member.token, nil, &readState); err != nil {
		return err
	}
	readSeq := readState.Conversation.LastReadSeq
	if readSeq <= 0 || readSeq > readState.Conversation.MaxSeq {
		return errors.New("read position exceeded current conversation tail")
	}
	if err := d.request(http.MethodPut, path+"/read", member.token, map[string]any{"seq": 1}, nil); err != nil {
		return err
	}
	var staleRead struct {
		Conversation conversationView `json:"conversation"`
	}
	if err := d.request(http.MethodGet, path, member.token, nil, &staleRead); err != nil {
		return err
	}
	if staleRead.Conversation.LastReadSeq != readSeq {
		return errors.New("read position moved backwards")
	}
	readFence, err := d.sendMessage(member, convID, suffix+"_read_fence", seq)
	if err != nil {
		return err
	}
	seq = readFence.Seq
	if _, err := collect("own-read-converged", func(changes []inboxChange) bool {
		return has("message.new", readFence.MessageID)(changes)
	}); err != nil {
		return err
	}
	merged, err := d.inboxRead("own-read-merged", member, readPosition, 50)
	if err != nil {
		return err
	}
	readEntries := 0
	for _, change := range merged.Changes {
		if change.Kind == "read.updated" {
			readEntries++
			if change.LastReadSeq != readSeq || change.Conversation == nil || change.Conversation.LastReadSeq != readSeq || change.Conversation.UnreadCount != 0 {
				return errors.New("read position/unread snapshot disagree")
			}
		}
	}
	if readEntries != 1 {
		return fmt.Errorf("own reads must compact to one entry, got %d", readEntries)
	}
	var detail struct {
		Conversation conversationView `json:"conversation"`
	}
	if err := d.request(http.MethodGet, path, member.token, nil, &detail); err != nil {
		return err
	}
	listed, err := d.conversationList("own-read-list", member, convID, true)
	if err != nil {
		return err
	}
	if detail.Conversation.LastReadSeq != readSeq || detail.Conversation.UnreadCount != 0 || listed.LastReadSeq != readSeq || listed.UnreadCount != 0 {
		return errors.New("list/detail/read positions disagree")
	}
	var receipt struct {
		Users []struct {
			UserID      decimal `json:"user_id"`
			LastReadSeq decimal `json:"last_read_seq"`
		} `json:"read_users"`
	}
	if err := d.request(http.MethodGet, path+"/read_status/"+sent[0].MessageID.String(), member.token, nil, &receipt); err != nil {
		return err
	}
	foundRead := false
	for _, user := range receipt.Users {
		if user.UserID == member.id && user.LastReadSeq == readSeq {
			foundRead = true
		}
	}
	if !foundRead {
		return errors.New("read receipt disagrees with list/detail")
	}
	if err := d.request(http.MethodPut, path+"/read", owner.token, map[string]any{"seq": seq}, nil); err != nil {
		return err
	}
	// Publish a later message to establish that the owner's read was consumed.
	marker, err := d.sendMessage(owner, convID, suffix+"_after_other_read", seq)
	if err != nil {
		return err
	}
	otherChanges, err := collect("other-read-not-in-inbox", has("message.new", marker.MessageID))
	if err != nil {
		return err
	}
	for _, change := range otherChanges {
		if change.Kind == "read.updated" {
			return errors.New("another user's read entered own inbox")
		}
	}
	// Removal reaches the removed recipient, then later messages do not.
	if err := d.request(http.MethodPost, path+"/members/kick", owner.token, map[string]any{"user_ids": []int64{int64(member.id)}}, nil); err != nil {
		return err
	}
	if _, err := collect("offline-removed", has("conversation.removed", 0)); err != nil {
		return err
	}
	removedPosition := position
	ownerState, err := d.inboxRead("owner-tail", owner, 0, 50)
	if err != nil {
		return err
	}
	afterRemoval, err := d.sendMessage(owner, convID, suffix+"_after_removal", marker.Seq)
	if err != nil {
		return err
	}
	if _, err := d.inboxDrain("after-removal-published", owner, ownerState.NextPosition, []sentMessage{afterRemoval}, false); err != nil {
		return err
	}
	removed, err := d.inboxRead("removed-no-further-message", member, removedPosition, 50)
	if err != nil {
		return err
	}
	if len(removed.Changes) != 0 {
		return errors.New("removed member received conversation increments")
	}
	if err := d.request(http.MethodPost, path+"/members/invite", owner.token, map[string]any{"user_ids": []int64{int64(member.id)}}, nil); err != nil {
		return err
	}
	if _, err := collect("offline-rejoined", has("conversation.upsert", 0)); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, path, owner.token, nil, nil); err != nil {
		return err
	}
	if _, err := collect("offline-dissolved", has("conversation.removed", 0)); err != nil {
		return err
	}
	return nil
}
