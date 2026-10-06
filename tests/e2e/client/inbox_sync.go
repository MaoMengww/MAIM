package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/maomeng/aim/pkg/sequence"
)

// 公开 HTTP 合同：实体为 UUID，位点为安全整数 JSON number。
type inboxChange struct {
	Position       sequenceNumber     `json:"position"`
	ConversationID entityID           `json:"conversation_id"`
	Kind           string             `json:"kind"`
	Message        storedMessage      `json:"message"`
	MessageID      *entityID          `json:"message_id"`
	Conversation   *inboxConversation `json:"conversation"`
	LastReadSeq    sequenceNumber     `json:"last_read_seq"`
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
	NextPosition    sequenceNumber  `json:"next_position"`
	RebuildRequired bool            `json:"rebuild_required"`
	Conversations   []inboxSnapshot `json:"conversations"`
	RebuildReason   string          `json:"rebuild_reason"`
}

func (d *driver) inboxRead(step string, caller account, position sequenceNumber, limit int) (inboxSyncResult, error) {
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
		if change.Position <= previous || change.ConversationID == "" {
			return result, fmt.Errorf("user-sync.%s: 变化位点必须递增且包含会话", step)
		}
		switch change.Kind {
		case "message.new", "message.edited", "message.recalled":
			if change.Message.ConvID != change.ConversationID || change.Message.MessageID == "" {
				return result, fmt.Errorf("user-sync.%s: 消息变更缺少完整消息", step)
			}
		case "message.deleted":
			if change.MessageID == nil {
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
func (d *driver) inboxDrain(step string, caller account, position sequenceNumber, expected []sentMessage, requirePagination bool) (sequenceNumber, error) {
	pending := make(map[entityID]sentMessage, len(expected))
	for _, message := range expected {
		pending[message.MessageID] = message
	}
	seen := make(map[entityID]bool, len(expected))
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

func checkInboxSnapshots(step string, result inboxSyncResult, expected map[entityID][]sentMessage, groupID entityID, groupName string) error {
	if len(result.Conversations) != len(expected) {
		return fmt.Errorf("user-sync.%s: 完整会话列表期望 %d 项，实际 %d 项（不得泄露其他用户会话）", step, len(expected), len(result.Conversations))
	}
	seen := make(map[entityID]bool, len(expected))
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

func (d *driver) userSync(addressA, addressB string) error {
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
	for _, query := range []string{"position=garbage&limit=1", "position=-1&limit=1", "position=1.5&limit=1", "position=9007199254740992&limit=1", "position=9223372036854775808&limit=1", "position=0&limit=-1", "position=0&limit=2147483648"} {
		err := d.request(http.MethodGet, "/messages/sync?"+query, receiver.token, nil, nil)
		var rejected *apiError
		if !errors.As(err, &rejected) || rejected.status != http.StatusBadRequest || rejected.code == 0 {
			return fmt.Errorf("user-sync.invalid-query: %s 期望HTTP400参数拒绝，实际 %v", query, err)
		}
	}
	senderInitial, err := d.inboxRead("sender-initial", sender, 0, 1)
	if err != nil {
		return err
	}
	if err := checkInboxRebuild("sender-initial", "new_device", senderInitial); err != nil {
		return err
	}
	create := func(step string, input map[string]any) (entityID, error) {
		var result struct {
			ID entityID `json:"conversation_id"`
		}
		if err := d.request(http.MethodPost, "/convs", sender.token, input, &result); err != nil {
			return "", fmt.Errorf("user-sync.%s: %w", step, err)
		}
		if result.ID == "" {
			return "", fmt.Errorf("user-sync.%s: 没有有效 conversation_id", step)
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
	history := map[entityID][]sentMessage{singleID: nil, groupID: nil}
	for i := range 3 {
		for _, convID := range []entityID{singleID, groupID} {
			previous := sequenceNumber(0)
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
	latest := map[entityID][]sentMessage{singleID: history[singleID][1:], groupID: history[groupID][1:]}
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
	unknown, err := d.inboxRead("unknown-position", receiver, sequenceNumber(sequence.Max), 2)
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
	if _, err := d.inboxDrain("unknown-rebuild-resume", receiver, unknown.NextPosition, nil, false); err != nil {
		return err
	}
	return d.personalDeletion(addressA, addressB, sender, receiver, history[singleID][2])
}

// Each device authenticates independently; no database/Kafka side channel is used.
func (d *driver) inboxLoginDevice(user account, label string) (account, error) {
	device := user
	device.device += "_" + label
	var result authResult
	if err := d.request(http.MethodPost, "/auth/login", "", map[string]string{
		"account": user.username, "password": user.password, "device_id": device.device, "platform": "web",
	}, &result); err != nil {
		return device, fmt.Errorf("personal-deletion.login-device: %w", err)
	}
	if result.UserID != user.id || result.User.ID != user.id || result.User.Username != user.username || result.Tokens.AccessToken == "" {
		return device, errors.New("personal-deletion.login-device: 第二设备未取得同账号独立鉴权身份")
	}
	device.token = result.Tokens.AccessToken
	return device, nil
}

func (d *driver) inboxLegacySyncAbsent(user account, convID entityID) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	// The removed route may return a plain-text 404, unlike the API envelope.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.gateway+"/api/v1/messages/"+convID.String()+"/sync?last_seq=0", nil)
	if err != nil {
		return errors.New("personal-deletion.legacy-sync: 无法创建请求")
	}
	req.Header.Set("Authorization", "Bearer "+user.token)
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("personal-deletion.legacy-sync: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("personal-deletion.legacy-sync: 旧会话同步URL期望HTTP404，实际%d", resp.StatusCode)
	}
	return nil
}

type inboxSearchResult struct {
	Messages   []storedMessage   `json:"messages"`
	Highlights map[string]string `json:"highlights"`
	Pagination struct {
		Total int64 `json:"total,string"`
	} `json:"pagination"`
	TypeCounts []struct {
		Count int64 `json:"count,string"`
	} `json:"type_counts"`
}

func (d *driver) inboxSearch(user account, convID entityID, keyword string) (inboxSearchResult, error) {
	var result inboxSearchResult
	path := "/messages/search?conversation_id=" + convID.String() + "&keyword=" + url.QueryEscape(keyword) + "&page=1&page_size=1"
	err := d.request(http.MethodGet, path, user.token, nil, &result)
	return result, err
}

func (d *driver) inboxSearchVisible(user account, expected sentMessage) (storedMessage, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		bounded := *d
		bounded.timeout = time.Until(deadline)
		result, err := bounded.inboxSearch(user, expected.ConvID, expected.Content.Text)
		if err != nil {
			return storedMessage{}, err
		}
		if len(result.Messages) != 0 {
			if len(result.Messages) != 1 || result.Pagination.Total != 1 {
				observed := make([]string, 0, len(result.Messages))
				for _, message := range result.Messages {
					observed = append(observed, message.MessageID.String()+":"+message.Text.Text)
				}
				return storedMessage{}, fmt.Errorf("personal-deletion.search: 唯一原文关键词 %q 的搜索结果/总数不精确: messages=%d total=%d hits=%v",
					expected.Content.Text, len(result.Messages), result.Pagination.Total, observed)
			}
			if err := checkStoredMessage("personal-deletion.search", result.Messages[0], expected); err != nil {
				return storedMessage{}, err
			}
			return result.Messages[0], nil
		}
		time.Sleep(min(100*time.Millisecond, max(0, time.Until(deadline))))
	}
	return storedMessage{}, errors.New("personal-deletion.search: 已确认消息未进入公开搜索结果")
}

func (d *driver) inboxMessageAbsent(step string, user account, messageID entityID) error {
	err := d.request(http.MethodGet, "/messages/"+messageID.String(), user.token, nil, nil)
	var rejected *apiError
	if !errors.As(err, &rejected) || rejected.status != http.StatusNotFound {
		return fmt.Errorf("personal-deletion.%s: 已删除消息byID期望HTTP404，实际%v", step, err)
	}
	return nil
}

func checkPersonalMessages(step string, messages []storedMessage, hidden sentMessage) error {
	for _, message := range messages {
		if message.MessageID == hidden.MessageID {
			return fmt.Errorf("personal-deletion.%s: 个人删除消息正文复活 message_id=%s", step, hidden.MessageID)
		}
		if reply := message.ReplyTo; reply != nil && reply.MessageID == hidden.MessageID {
			if !reply.Deleted || reply.Preview != "" || reply.SenderID != nil || reply.SenderName != "" || reply.SenderType != "" {
				return fmt.Errorf("personal-deletion.%s: 回复摘要泄露已个人删除的原文/发送者", step)
			}
		}
	}
	return nil
}

func (d *driver) inboxPersonalCatchup(user account, position sequenceNumber, hidden sentMessage) (sequenceNumber, error) {
	deadline := time.Now().Add(d.timeout)
	deleted := false
	for time.Now().Before(deadline) {
		bounded := *d
		bounded.timeout = time.Until(deadline)
		page, err := bounded.inboxRead("personal-delete-catchup", user, position, 1)
		if err != nil {
			return position, err
		}
		if page.RebuildRequired {
			return position, errors.New("personal-deletion.catchup: 删除前有效位点意外触发重建")
		}
		for _, change := range page.Changes {
			if err := checkPersonalMessages("catchup", []storedMessage{change.Message}, hidden); err != nil {
				return position, err
			}
			if change.Conversation != nil && hasEntityID(change.Conversation.LastMessageID, hidden.MessageID) {
				return position, errors.New("personal-deletion.catchup: 会话变化预览仍引用个人删除消息")
			}
			deleted = deleted || (change.Kind == "message.deleted" && change.ConversationID == hidden.ConvID && hasEntityID(change.MessageID, hidden.MessageID))
		}
		position = page.NextPosition
		if !page.HasMore && deleted {
			return position, nil
		}
		time.Sleep(min(50*time.Millisecond, max(0, time.Until(deadline))))
	}
	return position, errors.New("personal-deletion.catchup: 删除前位点未收到message.deleted tombstone")
}

func (d *driver) personalDeletion(addressA, addressB string, sender, receiver account, fallback sentMessage) error {
	convID := fallback.ConvID
	if err := d.inboxLegacySyncAbsent(receiver, convID); err != nil {
		return err
	}
	mirror, err := d.inboxLoginDevice(receiver, "mirror")
	if err != nil {
		return err
	}
	var sockets []*p6Socket
	for _, target := range []struct {
		user    account
		address string
	}{{receiver, addressA}, {mirror, addressB}, {sender, addressB}} {
		socket, err := d.p6Connect(target.address, target.user)
		if err != nil {
			return err
		}
		defer socket.close()
		sockets = append(sockets, socket)
	}
	before, err := d.inboxRead("personal-before-send", receiver, 0, 50)
	if err != nil {
		return err
	}
	senderBefore, err := d.inboxRead("personal-sender-before-send", sender, 0, 50)
	if err != nil {
		return err
	}
	// The fences below search by message text and require exactly one hit, and the
	// stored preview is compared as a whole. The deployed ik_max_word analyzer
	// splits code-like text into single-character tokens, so both keywords here
	// are code-like and share those single-character tokens: a query that settled
	// for one shared token instead of the whole keyword would also return its
	// sibling. Both texts stay inside the stored preview length.
	baseline, err := d.sendMessage(sender, convID, "tok0e5b9d", fallback.Seq)
	if err != nil {
		return err
	}
	hidden, err := d.sendMessage(sender, convID, "ovl52e0b93d", baseline.Seq)
	if err != nil {
		return err
	}
	for _, socket := range sockets[:2] {
		if _, err := d.p6Message(socket, hidden); err != nil {
			return err
		}
	}
	// A positive search on both accounts fences asynchronous indexing, so absence
	// after deletion cannot pass merely because Elasticsearch missed the message.
	for _, user := range []account{sender, receiver} {
		if _, err := d.inboxSearchVisible(user, hidden); err != nil {
			return err
		}
	}
	position, err := d.inboxDrain("personal-before-delete", receiver, before.NextPosition, []sentMessage{baseline, hidden}, false)
	if err != nil {
		return err
	}
	senderPosition, err := d.inboxDrain("personal-sender-before-delete", sender, senderBefore.NextPosition, []sentMessage{baseline, hidden}, false)
	if err != nil {
		return err
	}
	messagePath := "/messages/" + hidden.MessageID.String()
	if err := d.conversationForbidden("personal.non-sender-global-delete", http.MethodDelete, messagePath, receiver, map[string]bool{"delete_for_all": true}); err != nil {
		return err
	}
	if err := d.conversationMessage("personal.global-denial-unchanged", receiver, hidden); err != nil {
		return err
	}
	var cursors []int
	for _, socket := range sockets {
		cursors = append(cursors, socket.cursor())
	}
	if err := d.request(http.MethodDelete, messagePath, receiver.token, map[string]bool{"delete_for_all": false}, nil); err != nil {
		return fmt.Errorf("personal-deletion.delete-other-sender: %w", err)
	}
	for i, socket := range sockets[:2] {
		if _, err := d.p6Wait(socket, cursors[i], "personal-delete", func(e p6Event) bool {
			return e.Type == "message.deleted" && hasEntityID(e.ConvID, convID) && (hasEntityID(e.MessageID, hidden.MessageID) || e.Message.MessageID == hidden.MessageID)
		}); err != nil {
			return err
		}
	}
	for _, checkpoint := range []struct {
		user     account
		position sequenceNumber
	}{{mirror, position}, {receiver, before.NextPosition}, {mirror, position}} {
		if _, err := d.inboxPersonalCatchup(checkpoint.user, checkpoint.position, hidden); err != nil {
			return err
		}
	}
	for _, user := range []account{receiver, mirror} {
		if err := d.inboxMessageAbsent("same-account-by-id", user, hidden.MessageID); err != nil {
			return err
		}
		result, err := d.inboxSearch(user, convID, hidden.Content.Text)
		if err != nil {
			return err
		}
		if len(result.Messages) != 0 || result.Pagination.Total != 0 || len(result.Highlights) != 0 || slices.ContainsFunc(result.TypeCounts, func(count struct {
			Count int64 `json:"count,string"`
		}) bool {
			return count.Count != 0
		}) {
			return fmt.Errorf("personal-deletion.search: 个人删除后关键词 %q 仍返回 messages=%d total=%d highlights=%d type_counts=%d",
				hidden.Content.Text, len(result.Messages), result.Pagination.Total, len(result.Highlights), len(result.TypeCounts))
		}
	}
	for _, expected := range []struct {
		user    account
		message sentMessage
	}{{receiver, baseline}, {mirror, baseline}, {sender, hidden}} {
		row, err := d.conversationList("personal-preview", expected.user, convID, true)
		if err != nil {
			return err
		}
		var detail struct {
			Conversation conversationView `json:"conversation"`
		}
		if err := d.request(http.MethodGet, "/convs/"+convID.String(), expected.user.token, nil, &detail); err != nil {
			return err
		}
		for _, view := range []conversationView{row, detail.Conversation} {
			if !hasEntityID(view.LastMessageID, expected.message.MessageID) || view.LastMessagePreview != expected.message.Content.Text || view.MaxSeq != hidden.Seq {
				return errors.New("personal-deletion.preview: 列表/详情未按用户回退预览，或错误改变会话seq")
			}
		}
	}
	if err := d.conversationMessage("personal.sender-still-visible", sender, hidden); err != nil {
		return err
	}
	if _, err := d.inboxSearchVisible(sender, hidden); err != nil {
		return err
	}
	// The sender still sees the original and may reply. The receiver must see the
	// reply itself, but never its personally hidden original via a cached summary.
	// The reply text is a search keyword of its own and the asserted rebuild tail,
	// so it stays a distinct word token inside the stored preview length.
	var reply sentMessage
	text := "引用回复"
	submissionKey, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	reply.SubmissionKey = submissionKey.String()
	if err := d.request(http.MethodPost, "/messages/send", sender.token, map[string]any{
		"conversation_id": convID.String(), "client_msg_id": submissionKey.String(), "content": map[string]string{"text": text}, "reply_to_msg_id": hidden.MessageID.String(),
	}, &reply); err != nil {
		return err
	}
	if reply.MessageID == "" || reply.ConvID != convID || reply.Seq <= hidden.Seq || reply.SenderID != sender.id || reply.Content.Text != text {
		return errors.New("personal-deletion.reply: 回复HTTP确认身份/正文/seq不符合请求")
	}
	for _, user := range []account{receiver, mirror, sender} {
		var direct struct {
			Message storedMessage `json:"message"`
		}
		if err := d.request(http.MethodGet, "/messages/"+reply.MessageID.String(), user.token, nil, &direct); err != nil {
			return err
		}
		if err := checkStoredMessage("personal.reply", direct.Message, reply); err != nil {
			return err
		}
		if !hasEntityID(direct.Message.ReplyToID, hidden.MessageID) || direct.Message.ReplyTo == nil || direct.Message.ReplyTo.MessageID != hidden.MessageID {
			return errors.New("personal-deletion.reply: 回复摘要缺失，无法验证隔离")
		}
		searched, err := d.inboxSearchVisible(user, reply)
		if err != nil {
			return err
		}
		var around struct {
			Messages []storedMessage `json:"messages"`
		}
		if err := d.request(http.MethodGet, "/messages/"+convID.String()+"/around/"+hidden.Seq.String(), user.token, nil, &around); err != nil {
			return err
		}
		if !slices.ContainsFunc(around.Messages, func(message storedMessage) bool { return message.MessageID == reply.MessageID }) {
			return errors.New("personal-deletion.around: 隐藏锚点附近的可见回复也被错误过滤")
		}
		if user.id == receiver.id {
			messages := append(around.Messages, direct.Message, searched)
			if err := checkPersonalMessages("by-id/search/around-history", messages, hidden); err != nil {
				return err
			}
		} else if direct.Message.ReplyTo.Deleted || direct.Message.ReplyTo.Preview != hidden.Content.Text || !slices.ContainsFunc(around.Messages, func(message storedMessage) bool { return message.MessageID == hidden.MessageID }) {
			return errors.New("personal-deletion.sender: 原发送者的历史/回复摘要被其他人的个人删除改变")
		}
	}
	fresh, err := d.inboxLoginDevice(receiver, "new_device")
	if err != nil {
		return err
	}
	if err := d.inboxMessageAbsent("new-device-by-id", fresh, hidden.MessageID); err != nil {
		return err
	}
	for _, request := range []struct {
		user     account
		position sequenceNumber
		reason   string
	}{{fresh, 0, "new_device"}, {receiver, sequenceNumber(sequence.Max), "unknown_position"}, {sender, 0, "new_device"}} {
		result, err := d.inboxRead("personal-rebuild", request.user, request.position, 50)
		if err != nil {
			return err
		}
		if err := checkInboxRebuild("personal-rebuild", request.reason, result); err != nil {
			return err
		}
		found := false
		for _, snapshot := range result.Conversations {
			if snapshot.Conversation.ID != convID {
				continue
			}
			if !hasEntityID(snapshot.Conversation.LastMessageID, reply.MessageID) || snapshot.Conversation.LastMessagePreview != reply.Content.Text || snapshot.Conversation.MaxSeq != reply.Seq {
				return errors.New("personal-deletion.rebuild: 重建预览/seq与可见回复不一致")
			}
			found = slices.ContainsFunc(snapshot.Messages, func(message storedMessage) bool { return message.MessageID == reply.MessageID })
			if request.user.id == receiver.id {
				if err := checkPersonalMessages("rebuild-history", snapshot.Messages, hidden); err != nil {
					return err
				}
			} else if !slices.ContainsFunc(snapshot.Messages, func(message storedMessage) bool { return message.MessageID == hidden.MessageID }) {
				return errors.New("personal-deletion.rebuild: 个人删除影响原发送者重建历史")
			}
		}
		if !found {
			return errors.New("personal-deletion.rebuild: 可见回复缺失，不能以空历史证明不复活")
		}
		if _, err := d.inboxDrain("personal-rebuild-resume", request.user, result.NextPosition, nil, false); err != nil {
			return err
		}
	}
	// Observe the sender's own stream through the same public publication fence.
	if _, err := d.inboxDrain("personal-sender-isolation", sender, senderPosition, []sentMessage{reply}, false); err != nil {
		return err
	}
	if _, err := d.inboxPersonalCatchup(mirror, position, hidden); err != nil {
		return err
	}
	time.Sleep(time.Second)
	sockets[2].mu.Lock()
	defer sockets[2].mu.Unlock()
	if !sockets[2].closedAt.IsZero() {
		return errors.New("personal-deletion.sender-ws: 观察连接已关闭，不能证明隔离")
	}
	for _, evt := range sockets[2].events[cursors[2]:] {
		if evt.Type == "message.deleted" && (hasEntityID(evt.MessageID, hidden.MessageID) || evt.Message.MessageID == hidden.MessageID) {
			return errors.New("personal-deletion.sender-ws: 个人删除被错误扇出给原发送者")
		}
	}
	return nil
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
		ID entityID `json:"conversation_id"`
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
	has := func(kind string, id entityID) func([]inboxChange) bool {
		return func(changes []inboxChange) bool {
			for _, change := range changes {
				if change.Kind == kind && change.ConversationID == convID && (id == "" || change.Message.MessageID == id || hasEntityID(change.MessageID, id)) {
					return true
				}
			}
			return false
		}
	}
	if _, err := collect("offline-created", has("conversation.upsert", "")); err != nil {
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
	seq := sequenceNumber(0)
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
			if change.Message.Text.Text != "edited_twice_"+suffix || change.Message.Type != 1 || !hasEntityID(change.Message.SenderID, owner.id) || change.Message.EditCount != 2 {
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
	// Recall keeps a visible recalled entity for both accounts; global deletion
	// hides the body for both, unlike the receiver-only personal overlay above.
	for _, user := range []account{owner, member} {
		var recalled struct {
			Message storedMessage `json:"message"`
		}
		if err := d.request(http.MethodGet, "/messages/"+sent[1].MessageID.String(), user.token, nil, &recalled); err != nil {
			return err
		}
		if recalled.Message.MessageID != sent[1].MessageID || recalled.Message.Status != 2 {
			return errors.New("inbox-changes.recall: 撤回必须仍是可读取的撤回状态实体，而非个人/全员删除")
		}
		if err := d.inboxMessageAbsent("global-delete-both-accounts", user, sent[2].MessageID); err != nil {
			return err
		}
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
	for _, read := range []sequenceNumber{1, 2, 1, 999} {
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
			UserID      entityID       `json:"user_id"`
			LastReadSeq sequenceNumber `json:"last_read_seq"`
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
	if err := d.request(http.MethodPost, path+"/members/kick", owner.token, map[string]any{"user_ids": []entityID{member.id}}, nil); err != nil {
		return err
	}
	if _, err := collect("offline-removed", has("conversation.removed", "")); err != nil {
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
	if err := d.request(http.MethodPost, path+"/members/invite", owner.token, map[string]any{"user_ids": []entityID{member.id}}, nil); err != nil {
		return err
	}
	if _, err := collect("offline-rejoined", has("conversation.upsert", "")); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, path, owner.token, nil, nil); err != nil {
		return err
	}
	if _, err := collect("offline-dissolved", has("conversation.removed", "")); err != nil {
		return err
	}
	return nil
}
