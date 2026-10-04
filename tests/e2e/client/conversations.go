package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/websocket"
)

// These shapes mirror gateway protojson responses, not internal RPC clients.
// IDs and sequence numbers continue to use the driver's exact decimal decoder.
type conversationView struct {
	ID                 decimal `json:"id"`
	Type               int32   `json:"type"`
	Name               string  `json:"name"`
	OwnerID            decimal `json:"owner_id"`
	MemberCount        int32   `json:"member_count"`
	MaxSeq             decimal `json:"max_seq"`
	LastMessageID      decimal `json:"last_message_id"`
	LastMessagePreview string  `json:"last_message_preview"`
	LastReadSeq        decimal `json:"last_read_seq"`
	UnreadCount        int32   `json:"unread_count"`
}

type storedMessage struct {
	MessageID decimal `json:"message_id"`
	ConvID    decimal `json:"conversation_id"`
	SenderID  decimal `json:"from_user_id"`
	Seq       decimal `json:"seq"`
	Text      struct {
		Text string `json:"text"`
	} `json:"text"`
}

// conversations exercises only real authenticated HTTP and WebSocket surfaces.
// The optional unread branch is issue09 acceptance, intentionally not in all.
func (d *driver) conversations(address string, unread bool) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("owner", suffix)
	if err != nil {
		return err
	}
	member, err := d.register("member", suffix)
	if err != nil {
		return err
	}
	invitee, err := d.register("invitee", suffix)
	if err != nil {
		return err
	}
	if owner.id == member.id || owner.id == invitee.id || member.id == invitee.id {
		return errors.New("conversations.identity: 三个账号必须有不同 user_id")
	}
	name := "e2e_group_" + suffix
	var created struct {
		ID           decimal          `json:"conversation_id"`
		Conversation conversationView `json:"conversation"`
	}
	if err := d.conversationCall("create", http.MethodPost, "/convs", owner, map[string]any{
		"type": "group", "group_name": name, "member_ids": []string{member.id.String()},
	}, &created); err != nil {
		return err
	}
	convID := created.ID
	if convID <= 0 || created.Conversation.ID != convID || created.Conversation.Type != 2 || created.Conversation.Name != name || created.Conversation.OwnerID != owner.id {
		return errors.New("conversations.create: 返回的群聊 ID/类型/名称/群主不符合请求")
	}
	path := "/convs/" + convID.String()
	if err := d.conversationMembers("created", owner, convID, owner.id, member.id); err != nil {
		return err
	}
	for _, caller := range []account{owner, member} {
		if _, err := d.conversationList("created.visible", caller, convID, true); err != nil {
			return err
		}
	}
	if _, err := d.conversationList("outsider.hidden", invitee, convID, false); err != nil {
		return err
	}
	seed, err := d.sendMessage(owner, convID, suffix+"_before_invite", 0)
	if err != nil {
		return err
	}
	if err := d.conversationMessage("member.read", member, seed); err != nil {
		return err
	}
	if err := d.conversationSync("member.sync", member, convID, 0, seed); err != nil {
		return err
	}
	for _, target := range []string{path, "/messages/" + seed.MessageID.String(), "/messages/" + convID.String() + "/sync"} {
		if err := d.conversationForbidden("outsider.read", http.MethodGet, target, invitee, nil); err != nil {
			return err
		}
	}
	inviteBody := map[string]any{"user_ids": []int64{int64(invitee.id)}}
	if err := d.conversationForbidden("member.invite", http.MethodPost, path+"/members/invite", member, inviteBody); err != nil {
		return err
	}
	if err := d.conversationMembers("invite.denied.unchanged", owner, convID, owner.id, member.id); err != nil {
		return err
	}
	var invited struct {
		Added  []decimal `json:"added_user_ids"`
		Failed []decimal `json:"failed_user_ids"`
	}
	if err := d.conversationCall("owner.invite", http.MethodPost, path+"/members/invite", owner, inviteBody, &invited); err != nil {
		return err
	}
	if !slices.Equal(invited.Added, []decimal{invitee.id}) || len(invited.Failed) != 0 {
		return errors.New("conversations.owner.invite: 必须成功新增指定用户且无失败用户")
	}
	if err := d.conversationMembers("invited", invitee, convID, owner.id, member.id, invitee.id); err != nil {
		return err
	}
	if _, err := d.conversationList("invited.visible", invitee, convID, true); err != nil {
		return err
	}
	if err := d.conversationForbidden("member.kick", http.MethodPost, path+"/members/kick", member, inviteBody); err != nil {
		return err
	}
	if err := d.conversationMembers("kick.denied.unchanged", owner, convID, owner.id, member.id, invitee.id); err != nil {
		return err
	}
	connB, err := d.connect(address, member)
	if err != nil {
		return fmt.Errorf("conversations.ws.member: %w", err)
	}
	defer connB.Close()
	connC, err := d.connect(address, invitee)
	if err != nil {
		return fmt.Errorf("conversations.ws.invitee: %w", err)
	}
	defer connC.Close()
	for _, conn := range []*websocket.Conn{connB, connC} {
		if err := d.ready(conn); err != nil {
			return fmt.Errorf("conversations.ws.ready: %w", err)
		}
	}
	first, err := d.sendMessage(owner, convID, suffix+"_group_first", seed.Seq)
	if err != nil {
		return err
	}
	for _, conn := range []*websocket.Conn{connB, connC} {
		if err := d.receiveMessage(conn, first); err != nil {
			return fmt.Errorf("conversations.ws.first: %w", err)
		}
	}
	latest, err := d.sendMessage(owner, convID, suffix+"_group_latest", first.Seq)
	if err != nil {
		return err
	}
	for _, conn := range []*websocket.Conn{connB, connC} {
		if err := d.receiveMessage(conn, latest); err != nil {
			return fmt.Errorf("conversations.ws.latest: %w", err)
		}
	}
	if err := d.conversationMessage("invited.read", invitee, latest); err != nil {
		return err
	}
	if err := d.conversationSync("invited.catchup", invitee, convID, seed.Seq, first, latest); err != nil {
		return err
	}
	if err := d.conversationSync("invited.resume", invitee, convID, first.Seq, latest); err != nil {
		return err
	}
	for _, caller := range []account{owner, member, invitee} {
		row, err := d.conversationList("messages.visible", caller, convID, true)
		if err != nil {
			return err
		}
		if row.MaxSeq != latest.Seq || row.LastMessageID != latest.MessageID || row.MemberCount != 3 {
			return fmt.Errorf("conversations.messages.visible: user_id=%s 列表 max_seq/last_message_id/成员数与已确认消息不一致", caller.id)
		}
	}
	if err := d.conversationReceipt("before.read", owner, invitee, latest, false); err != nil {
		return err
	}
	if unread {
		if err := d.conversationUnread("before.read", invitee, convID, latest.Seq, false); err != nil {
			return err
		}
	}
	if err := d.conversationCall("mark.read", http.MethodPut, path+"/read", invitee, map[string]any{"seq": int64(latest.Seq)}, nil); err != nil {
		return err
	}
	if err := d.conversationReceipt("after.read", owner, invitee, latest, true); err != nil {
		return err
	}
	row, err := d.conversationList("read.position", invitee, convID, true)
	if err != nil {
		return err
	}
	if row.LastReadSeq != latest.Seq {
		return fmt.Errorf("conversations.read.position: 期望 seq=%s，实际=%s", latest.Seq, row.LastReadSeq)
	}
	if unread {
		if err := d.conversationUnread("after.read", invitee, convID, latest.Seq, true); err != nil {
			return err
		}
		if err := d.conversationCall("mark.older", http.MethodPut, path+"/read", invitee, map[string]any{"seq": int64(first.Seq)}, nil); err != nil {
			return err
		}
		if err := d.conversationUnread("no.regression", invitee, convID, latest.Seq, true); err != nil {
			return err
		}
		if err := d.conversationReceipt("no.regression", owner, invitee, latest, true); err != nil {
			return err
		}
	}
	if err := d.conversationCall("owner.kick", http.MethodPost, path+"/members/kick", owner, inviteBody, nil); err != nil {
		return err
	}
	if err := d.conversationMembers("removed", owner, convID, owner.id, member.id); err != nil {
		return err
	}
	if _, err := d.conversationList("removed.hidden", invitee, convID, false); err != nil {
		return err
	}
	for _, target := range []string{path, "/messages/" + latest.MessageID.String(), "/messages/" + convID.String() + "/sync"} {
		if err := d.conversationForbidden("removed.read", http.MethodGet, target, invitee, nil); err != nil {
			return err
		}
	}
	// Positive reads on the same endpoints distinguish permissions from outages.
	if err := d.conversationMessage("remaining.read", member, latest); err != nil {
		return err
	}
	if err := d.conversationSync("remaining.sync", member, convID, first.Seq, latest); err != nil {
		return err
	}
	for _, caller := range []account{owner, member} {
		if _, err := d.conversationList("remaining.visible", caller, convID, true); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) conversationCall(step, method, path string, caller account, input, output any) error {
	if err := d.request(method, path, caller.token, input, output); err != nil {
		return fmt.Errorf("conversations.%s user_id=%s: %w", step, caller.id, err)
	}
	return nil
}

func (d *driver) conversationForbidden(step, method, path string, caller account, input any) error {
	err := d.request(method, path, caller.token, input, nil)
	var rejected *apiError
	if errors.As(err, &rejected) && rejected.status == http.StatusForbidden && rejected.code != 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("conversations.%s user_id=%s: 期望 HTTP403 权限拒绝而非服务/传输错误: %w", step, caller.id, err)
	}
	return fmt.Errorf("conversations.%s user_id=%s: 无权限操作 %s %s 却成功", step, caller.id, method, path)
}

func (d *driver) conversationMembers(step string, caller account, convID decimal, users ...decimal) error {
	var result struct {
		Members []struct {
			UserID decimal `json:"user_id"`
		} `json:"members"`
	}
	if err := d.conversationCall(step+".members", http.MethodGet, "/convs/"+convID.String()+"/members", caller, nil, &result); err != nil {
		return err
	}
	actual := make([]decimal, len(result.Members))
	for i, member := range result.Members {
		actual[i] = member.UserID
	}
	slices.Sort(actual)
	slices.Sort(users)
	if !slices.Equal(actual, users) {
		return fmt.Errorf("conversations.%s.members: 期望成员=%v，实际=%v", step, users, actual)
	}
	return nil
}

func (d *driver) conversationList(step string, caller account, convID decimal, visible bool) (conversationView, error) {
	var result struct {
		Conversations []conversationView `json:"conversations"`
	}
	if err := d.conversationCall(step+".list", http.MethodGet, "/convs", caller, nil, &result); err != nil {
		return conversationView{}, err
	}
	var found conversationView
	count := 0
	for _, row := range result.Conversations {
		if row.ID == convID {
			found = row
			count++
		}
	}
	if (visible && count != 1) || (!visible && count != 0) {
		return found, fmt.Errorf("conversations.%s.list: user_id=%s conv_id=%s 期望可见=%t，实际条目数=%d", step, caller.id, convID, visible, count)
	}
	return found, nil
}

func (d *driver) conversationMessage(step string, caller account, expected sentMessage) error {
	var result struct {
		Message storedMessage `json:"message"`
	}
	if err := d.conversationCall(step, http.MethodGet, "/messages/"+expected.MessageID.String(), caller, nil, &result); err != nil {
		return err
	}
	return checkStoredMessage(step, result.Message, expected)
}

func checkStoredMessage(step string, actual storedMessage, expected sentMessage) error {
	if actual.MessageID != expected.MessageID || actual.ConvID != expected.ConvID || actual.SenderID != expected.SenderID || actual.Seq != expected.Seq || actual.Text.Text != expected.Content.Text {
		return fmt.Errorf("conversations.%s: 持久化消息与 HTTP 确认不一致，期望 message_id=%s conv_id=%s seq=%s sender_id=%s", step, expected.MessageID, expected.ConvID, expected.Seq, expected.SenderID)
	}
	return nil
}

func (d *driver) conversationSync(step string, caller account, convID, after decimal, expected ...sentMessage) error {
	deadline := time.Now().Add(d.timeout)
	var result struct {
		Messages []storedMessage `json:"messages"`
		MaxSeq   decimal         `json:"max_seq"`
		HasMore  bool            `json:"has_more"`
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("conversations.%s: 等待收件箱补拉超时", step)
		}
		bounded := *d
		bounded.timeout = remaining
		path := "/messages/" + convID.String() + "/sync?from_seq=" + after.String() + "&limit=50"
		if err := bounded.conversationCall(step, http.MethodGet, path, caller, nil, &result); err != nil {
			return err
		}
		previous, matched := after, 0
		for _, msg := range result.Messages {
			if msg.ConvID != convID || msg.Seq <= previous {
				return fmt.Errorf("conversations.%s: 补拉包含其他会话或 seq 未严格递增/越过 from_seq=%s", step, after)
			}
			previous = msg.Seq
			if matched < len(expected) && msg.MessageID == expected[matched].MessageID {
				if err := checkStoredMessage(step, msg, expected[matched]); err != nil {
					return err
				}
				matched++
			}
		}
		if matched != len(expected) {
			time.Sleep(min(100*time.Millisecond, max(0, time.Until(deadline))))
			continue
		}
		if result.HasMore || result.MaxSeq < previous {
			return fmt.Errorf("conversations.%s: 补拉分页状态不一致（has_more=%t max_seq=%s last_seq=%s）", step, result.HasMore, result.MaxSeq, previous)
		}
		return nil
	}
}

func (d *driver) conversationReceipt(step string, caller, reader account, message sentMessage, read bool) error {
	var result struct {
		ReadCount  int32 `json:"read_count"`
		TotalCount int32 `json:"total_count"`
		Users      []struct {
			UserID      decimal `json:"user_id"`
			LastReadSeq decimal `json:"last_read_seq"`
		} `json:"read_users"`
	}
	path := "/convs/" + message.ConvID.String() + "/read_status/" + message.MessageID.String()
	if err := d.conversationCall(step+".receipt", http.MethodGet, path, caller, nil, &result); err != nil {
		return err
	}
	if result.TotalCount != 3 || result.ReadCount != int32(len(result.Users)) {
		return fmt.Errorf("conversations.%s.receipt: 成员总数/已读数量与回执用户列表不一致", step)
	}
	seen := make(map[decimal]bool, len(result.Users))
	found := false
	for _, user := range result.Users {
		if user.UserID <= 0 || seen[user.UserID] || user.LastReadSeq < message.Seq {
			return fmt.Errorf("conversations.%s.receipt: 已读用户重复/无效或位点未覆盖消息 seq=%s", step, message.Seq)
		}
		seen[user.UserID] = true
		if user.UserID == reader.id {
			found = true
			if user.LastReadSeq != message.Seq {
				return fmt.Errorf("conversations.%s.receipt: user_id=%s 位点=%s，期望=%s", step, reader.id, user.LastReadSeq, message.Seq)
			}
		}
	}
	if found != read {
		return fmt.Errorf("conversations.%s.receipt: message_id=%s user_id=%s 期望已读=%t，实际=%t", step, message.MessageID, reader.id, read, found)
	}
	return nil
}

// Poll the public unread projection under one shared deadline, including every
// HTTP request and wait.
func (d *driver) conversationUnread(step string, caller account, convID, seq decimal, read bool) error {
	deadline := time.Now().Add(d.timeout)
	var row conversationView
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("conversations.%s.unread: user_id=%s 等待列表超时，unread_count=%d last_read_seq=%s，期望已读=%t seq=%s", step, caller.id, row.UnreadCount, row.LastReadSeq, read, seq)
		}
		bounded := *d
		bounded.timeout = remaining
		var err error
		row, err = bounded.conversationList(step+".unread", caller, convID, true)
		if err != nil {
			return err
		}
		if row.UnreadCount < 0 {
			return fmt.Errorf("conversations.%s.unread: 未读数不能为负数", step)
		}
		if read && row.LastReadSeq != seq {
			return fmt.Errorf("conversations.%s.unread: 已读位点期望=%s，实际=%s（不能回退）", step, seq, row.LastReadSeq)
		}
		if (read && row.UnreadCount == 0) || (!read && row.UnreadCount >= 2 && row.LastReadSeq < seq) {
			return nil
		}
		time.Sleep(min(100*time.Millisecond, max(0, time.Until(deadline))))
	}
}
