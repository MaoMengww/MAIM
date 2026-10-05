package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Broadcast acceptance uses the same message.new, conversation and message
// surfaces as chat. Broadcast creation only acknowledges the administrative ID.
type broadcastContent struct {
	Action    string  `json:"action"`
	Detail    string  `json:"detail"`
	ActorID   decimal `json:"actor_id"`
	ActorType string  `json:"actor_type"`
	Payload   string  `json:"payload"`
}

type broadcastMessage struct {
	storedMessage
	Type   int32            `json:"type"`
	System broadcastContent `json:"system"`
}

type broadcastBody struct {
	text    string
	payload string
}

func (d *driver) broadcasts(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	users := make([]account, 4)
	for i := range users {
		users[i], err = d.register(fmt.Sprintf("broadcast_%d", i), suffix)
		if err != nil {
			return err
		}

	}
	var group struct {
		ID decimal `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", users[0].token, map[string]any{
		"type": "group", "group_name": "broadcast_group_" + suffix, "member_ids": []string{users[1].id.String()},
	}, &group); err != nil {
		return err
	}
	if group.ID <= 0 {
		return errors.New("broadcasts.group: 缺少有效群会话 ID")
	}
	if err := d.conversationMembers("broadcast.group", users[0], group.ID, users[0].id, users[1].id); err != nil {
		return err
	}
	connections := make([]*websocket.Conn, 3)
	for i, address := range []string{addressA, addressB, addressA} {
		connections[i], err = d.connect(address, users[i])
		if err != nil {
			return err
		}
		defer connections[i].Close()
		if err := d.ready(connections[i]); err != nil {
			return err
		}
	}
	// User D remains disconnected through all three scopes. The first two sends
	// race on A's first system conversation, rather than on an already-created row.
	first := newBroadcastBody(suffix + "_user_first")
	second := newBroadcastBody(suffix + "_user_concurrent")
	start := make(chan struct{})
	failures := make([]error, 2)
	var sends sync.WaitGroup
	for i, body := range []broadcastBody{first, second} {
		sends.Go(func() {
			<-start
			failures[i] = d.createBroadcast(users[2], "user", users[0].id, body)
		})
	}
	close(start)
	sends.Wait()
	if err := errors.Join(failures...); err != nil {
		return err
	}
	expected := make([][]broadcastBody, len(users))
	expected[0] = []broadcastBody{first, second}
	convIDs := make([]decimal, len(users))
	messages := make([][]broadcastMessage, len(users))
	if err := d.broadcastState(users, users[2], expected, convIDs, messages); err != nil {
		return fmt.Errorf("broadcasts.user: %w", err)
	}
	if err := d.broadcastReceive(connections[0], users[2], messages[0]); err != nil {
		return err
	}
	if err := d.conversationForbidden("broadcast.system.self.remove", http.MethodPost,
		"/convs/"+convIDs[0].String()+"/members/kick", users[0],
		map[string]any{"user_ids": []int64{int64(users[0].id)}}); err != nil {
		return err
	}
	groupBody := newBroadcastBody(suffix + "_group")
	if err := d.createBroadcast(users[2], "group", group.ID, groupBody); err != nil {
		return err
	}
	for _, i := range []int{0, 1} {
		expected[i] = append(expected[i], groupBody)
	}
	if err := d.broadcastState(users, users[2], expected, convIDs, messages); err != nil {
		return fmt.Errorf("broadcasts.group: %w", err)
	}
	for _, i := range []int{0, 1} {
		if err := d.broadcastReceive(connections[i], users[2], messages[i][len(messages[i])-1:]); err != nil {
			return err
		}
	}
	// B was outside the user scope and C was outside both targeted scopes.
	// Their ordinary list, sync and history projections above must contain no
	// targeted broadcast; their WS readers also reject unexpected broadcast IDs.
	allBody := newBroadcastBody(suffix + "_all")
	if err := d.createBroadcast(users[2], "all", 0, allBody); err != nil {
		return err
	}
	for i := range users {
		expected[i] = append(expected[i], allBody)
	}
	if err := d.broadcastState(users, users[2], expected, convIDs, messages); err != nil {
		return fmt.Errorf("broadcasts.all: %w", err)
	}
	for i, conn := range connections {
		if err := d.broadcastReceive(conn, users[2], messages[i][len(messages[i])-1:]); err != nil {
			return err
		}
	}
	connD, err := d.connect(addressB, users[3])
	if err != nil {
		return err
	}
	defer connD.Close()
	if err := d.ready(connD); err != nil {
		return err
	}
	// Reconnect uses the existing ordinary conversation sync until issue04.
	// Read the same ordinary surfaces after reconnect; no broadcast endpoint is
	// consulted to recover content or to determine which user owns a message.
	if err := d.broadcastState(users, users[2], expected, convIDs, messages); err != nil {
		return err
	}
	for i, caller := range users {
		owned := messages[i][0]
		var ownResult struct {
			Message broadcastMessage `json:"message"`
		}
		if err := d.request(http.MethodGet, "/messages/"+owned.MessageID.String(), caller.token, nil, &ownResult); err != nil {
			return err
		}
		if err := checkBroadcastMessage(ownResult.Message, owned, users[2]); err != nil {
			return err
		}
		other := (i + 1) % len(users)
		foreign := messages[other][0]
		for _, path := range []string{
			"/convs/" + convIDs[other].String(),
			"/messages/" + foreign.MessageID.String(),
			"/messages/" + convIDs[other].String() + "/around/" + foreign.Seq.String(),
		} {
			if err := d.conversationForbidden("broadcast.isolation", http.MethodGet, path, caller, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func newBroadcastBody(text string) broadcastBody {
	// Deliberate whitespace, non-ASCII and an extra field ensure payload is the
	// original string, not a decoded/re-encoded JSON approximation.
	raw, _ := json.Marshal(map[string]string{"text": text + "_通知", "marker": "原样"})
	return broadcastBody{text: text + "_通知", payload: "  " + string(raw) + "\n"}
}

func (d *driver) createBroadcast(sender account, scope string, target decimal, body broadcastBody) error {
	request := map[string]any{"sender_id": sender.id.String(), "scope": scope, "content": body.payload}
	if target > 0 {
		request["scope_target_id"] = target.String()
	}
	var result struct {
		ID decimal `json:"broadcast_id"`
	}
	if err := d.request(http.MethodPost, "/broadcasts", sender.token, request, &result); err != nil {
		return fmt.Errorf("broadcasts.create.%s: %w", scope, err)
	}
	if result.ID <= 0 {
		return errors.New("broadcasts.create: 缺少有效 broadcast_id")
	}
	return nil
}

// Match the existing conversationSync deadline polling pattern: each HTTP
// request shares the remaining deadline; pauses are retries, not proof by sleep.
func (d *driver) broadcastPoll(step string, observe func(*driver) (bool, error)) error {
	deadline := time.Now().Add(d.timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("broadcasts.%s: 等待普通消息投影收敛超时", step)
		}
		bounded := *d
		bounded.timeout = remaining
		done, err := observe(&bounded)
		if err != nil || done {
			return err
		}
		time.Sleep(min(100*time.Millisecond, max(0, time.Until(deadline))))
	}
}

func (d *driver) broadcastState(users []account, sender account, expected [][]broadcastBody, convIDs []decimal, messages [][]broadcastMessage) error {
	// First wait for recipients, then inspect outsiders. This avoids declaring
	// isolation from an empty projection before legitimate fanout has progressed.
	for _, visible := range []bool{true, false} {
		for i, caller := range users {
			if (len(expected[i]) > 0) != visible {
				continue
			}
			if err := d.broadcastPoll("state."+caller.id.String(), func(bounded *driver) (bool, error) {
				var listed struct {
					Conversations []conversationView `json:"conversations"`
				}
				if err := bounded.request(http.MethodGet, "/convs", caller.token, nil, &listed); err != nil {
					return false, err
				}
				var system []conversationView
				for _, row := range listed.Conversations {
					if row.Type == 3 {
						system = append(system, row)
					}
				}
				if len(system) > 1 || (!visible && len(system) != 0) {
					return false, errors.New("broadcasts.scope: 系统会话重复或范围外用户拥有系统会话")
				}
				if !visible {
					return bounded.broadcastIncremental(caller, sender, 0, nil)
				}
				if len(system) == 0 {
					return false, nil
				}
				row := system[0]
				if row.ID <= 0 || row.OwnerID != caller.id || row.MemberCount != 1 || (convIDs[i] != 0 && row.ID != convIDs[i]) {
					return false, errors.New("broadcasts.conversation: 系统会话 ID/归属/唯一成员/复用不符合契约")
				}
				for j, id := range convIDs {
					if i != j && id == row.ID {
						return false, errors.New("broadcasts.conversation: 不同收件人共用系统会话")
					}
				}
				convIDs[i] = row.ID
				if _, err := bounded.conversationList("broadcast.visible", caller, row.ID, true); err != nil {
					return false, err
				}
				var members struct {
					Members []struct {
						UserID decimal `json:"user_id"`
						Role   int32   `json:"role"`
					} `json:"members"`
				}
				if err := bounded.request(http.MethodGet, "/convs/"+row.ID.String()+"/members", caller.token, nil, &members); err != nil {
					return false, err
				}
				if len(members.Members) != 1 || members.Members[0].UserID != caller.id || members.Members[0].Role != 3 {
					return false, errors.New("broadcasts.members: 收件用户必须是唯一 MEMBER，不能拥有群管理角色")
				}
				var history struct {
					Messages []broadcastMessage `json:"messages"`
				}
				path := "/messages/" + row.ID.String() + "/around/" + row.MaxSeq.String()
				if err := bounded.request(http.MethodGet, path, caller.token, nil, &history); err != nil {
					return false, err
				}
				if len(history.Messages) > len(expected[i]) {
					return false, errors.New("broadcasts.history: 系统会话包含额外广播或重复消息")
				}
				if len(history.Messages) < len(expected[i]) {
					return false, nil
				}
				slices.SortFunc(history.Messages, func(a, b broadcastMessage) int {
					return cmp.Compare(a.Seq, b.Seq)
				})
				seen := make(map[string]bool, len(expected[i]))
				ids := make(map[decimal]bool, len(expected[i]))
				var previous decimal
				for _, msg := range history.Messages {
					index := slices.IndexFunc(expected[i], func(body broadcastBody) bool { return msg.System.Detail == body.text })
					if index < 0 || seen[msg.System.Detail] || ids[msg.MessageID] || msg.ConvID != row.ID || msg.Seq <= previous {
						return false, errors.New("broadcasts.history: 正文/会话/消息 ID 不精确或 seq 重复/未递增")
					}
					if err := checkBroadcastContent(msg.System, expected[i][index], sender); err != nil {
						return false, err
					}
					if msg.MessageID <= 0 || msg.Type != 7 || msg.SenderID != sender.id {
						return false, errors.New("broadcasts.history: 缺少正消息 ID 或系统消息类型/发送者错误")
					}
					seen[msg.System.Detail], ids[msg.MessageID], previous = true, true, msg.Seq
				}
				latest := history.Messages[len(history.Messages)-1]
				if row.MaxSeq != latest.Seq || row.LastMessageID != latest.MessageID {
					return false, nil
				}
				for _, msg := range messages[i] {
					index := slices.IndexFunc(history.Messages, func(candidate broadcastMessage) bool { return candidate.MessageID == msg.MessageID })
					if index < 0 {
						return false, errors.New("broadcasts.reuse: 后续广播丢失此前消息")
					}
					if err := checkBroadcastMessage(history.Messages[index], msg, sender); err != nil {
						return false, err
					}
				}
				done, err := bounded.broadcastIncremental(caller, sender, row.ID, history.Messages)
				if done && err == nil {
					messages[i] = history.Messages
				}
				return done, err
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkBroadcastContent(actual broadcastContent, expected broadcastBody, sender account) error {
	if actual.Action != "broadcast" || actual.Detail != expected.text || actual.Payload != expected.payload || actual.ActorID != sender.id || actual.ActorType != "system" {
		return errors.New("broadcasts.content: system action/detail/actor/payload 与原请求不一致")
	}
	return nil
}

func checkBroadcastMessage(actual, expected broadcastMessage, sender account) error {
	if actual.MessageID != expected.MessageID || actual.ConvID != expected.ConvID || actual.Seq != expected.Seq || actual.SenderID != sender.id || actual.Type != 7 {
		return errors.New("broadcasts.message: 普通消息读取的 ID/会话/seq/类型/发送者与历史不一致")
	}
	return checkBroadcastContent(actual.System, broadcastBody{text: expected.System.Detail, payload: expected.System.Payload}, sender)
}

func (d *driver) broadcastIncremental(caller, sender account, convID decimal, expected []broadcastMessage) (bool, error) {
	if convID == 0 {
		return len(expected) == 0, nil
	}
	var page struct {
		Messages []broadcastMessage `json:"messages"`
	}
	if err := d.request(http.MethodGet, "/messages/"+convID.String()+"/sync?from_seq=0&limit=50", caller.token, nil, &page); err != nil {
		return false, err
	}
	if len(page.Messages) > len(expected) {
		return false, errors.New("broadcasts.sync: duplicate or unexpected message")
	}
	if len(page.Messages) < len(expected) {
		return false, nil
	}
	matched := make(map[decimal]bool, len(expected))
	for _, msg := range page.Messages {
		index := slices.IndexFunc(expected, func(candidate broadcastMessage) bool { return candidate.MessageID == msg.MessageID })
		if index < 0 || matched[msg.MessageID] {
			return false, errors.New("broadcasts.sync: unexpected or duplicate message")
		}
		if err := checkBroadcastMessage(msg, expected[index], sender); err != nil {
			return false, err
		}
		matched[msg.MessageID] = true
	}
	return true, nil
}

func (d *driver) broadcastReceive(conn *websocket.Conn, sender account, expected []broadcastMessage) error {
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return err
	}
	seen := make(map[decimal]bool, len(expected))
	for len(seen) < len(expected) {
		var evt struct {
			Type    string  `json:"type"`
			ConvID  decimal `json:"conv_id"`
			Message struct {
				MessageID  decimal          `json:"message_id"`
				ConvID     decimal          `json:"conv_id"`
				SenderID   decimal          `json:"sender_id"`
				SenderType string           `json:"sender_type"`
				Seq        decimal          `json:"seq"`
				MsgType    int32            `json:"msg_type"`
				Content    broadcastContent `json:"content"`
			} `json:"message"`
		}
		if err := conn.ReadJSON(&evt); err != nil {
			return fmt.Errorf("broadcasts.message.new: %s", transportFailure(err))
		}
		if evt.Type == "error" {
			return errors.New("broadcasts.message.new: WS 返回 error")
		}
		if evt.Type != "message.new" {
			continue
		}
		msg := evt.Message
		index := slices.IndexFunc(expected, func(candidate broadcastMessage) bool { return candidate.MessageID == msg.MessageID })
		if index < 0 || seen[msg.MessageID] {
			return errors.New("broadcasts.message.new: 收到范围外/非预期/重复普通消息")
		}
		confirmed := expected[index]
		if evt.ConvID <= 0 || evt.ConvID != confirmed.ConvID || msg.ConvID != confirmed.ConvID || msg.MessageID <= 0 || msg.Seq <= 0 || msg.Seq != confirmed.Seq || msg.SenderID != sender.id || msg.SenderType != "system" || msg.MsgType != 7 {
			return errors.New("broadcasts.message.new: 标准信封 conv/message/seq/type/发送者与 REST 历史不一致")
		}
		if err := checkBroadcastContent(msg.Content, broadcastBody{text: confirmed.System.Detail, payload: confirmed.System.Payload}, sender); err != nil {
			return err
		}
		seen[msg.MessageID] = true
	}
	return nil
}
