package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// p6Socket observes the real WS without read-timeout corruption: one reader
// retains events and disconnect times while an independent writer sends the
// public heartbeat. Negative membership checks use the same observation log.
type p6Event struct {
	event
	MessageID   *entityID      `json:"message_id"`
	UserID      *entityID      `json:"user_id"`
	LastReadSeq sequenceNumber `json:"last_read_seq"`
	UnreadCount *int32         `json:"unread_count"`
	StreamID    string         `json:"stream_id"`
	BotID       *entityID      `json:"bot_id"`
	ReplyToID   *entityID      `json:"reply_to_msg_id"`
	Content     string         `json:"content"`
	Seq         int64          `json:"seq"`
	Online      bool           `json:"online"`
	Devices     []struct {
		DeviceID   string `json:"device_id"`
		InstanceID string `json:"instance_id"`
	} `json:"devices"`
}

type p6Socket struct {
	conn     *websocket.Conn
	mu       sync.Mutex
	writeMu  sync.Mutex
	events   []p6Event
	closedAt time.Time
	readErr  error
	done     chan struct{}
}

func (d *driver) p6Connect(address string, user account) (*p6Socket, error) {
	conn, err := d.connect(address, user)
	if err != nil {
		return nil, err
	}
	if err := d.ready(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	s := &p6Socket{conn: conn, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			_, raw, err := conn.ReadMessage()
			var evt p6Event
			if err == nil {
				err = json.Unmarshal(raw, &evt)
			}
			s.mu.Lock()
			if err != nil {
				s.closedAt, s.readErr = time.Now(), err
				s.mu.Unlock()
				return
			}
			s.events = append(s.events, evt)
			s.mu.Unlock()
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				if s.send(map[string]string{"type": "ping"}, d.timeout) != nil {
					return
				}
			}
		}
	}()
	return s, nil
}

func (s *p6Socket) close() { _ = s.conn.Close() }

func (s *p6Socket) send(input any, timeout time.Duration) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if err := s.conn.WriteJSON(input); err != nil {
		return errors.New("p6.ws: 公开事件发送失败")
	}
	return nil
}

func (s *p6Socket) cursor() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (d *driver) p6Wait(s *p6Socket, after int, label string, match func(p6Event) bool) (p6Event, error) {
	deadline := time.Now().Add(d.timeout)
	for {
		s.mu.Lock()
		for _, evt := range s.events[after:] {
			if match(evt) {
				s.mu.Unlock()
				return evt, nil
			}
			if evt.Type == "error" {
				s.mu.Unlock()
				return p6Event{}, fmt.Errorf("p6.%s: WS error事件", label)
			}
		}
		closed, failure := s.closedAt, s.readErr
		s.mu.Unlock()
		if !closed.IsZero() {
			return p6Event{}, fmt.Errorf("p6.%s: 匹配事件前连接关闭（%s）", label, transportFailure(failure))
		}
		if time.Now().After(deadline) {
			return p6Event{}, fmt.Errorf("p6.%s: 公开WS事件超时", label)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (d *driver) p6Message(s *p6Socket, sent sentMessage) (p6Event, error) {
	evt, err := d.p6Wait(s, 0, "message.new", func(e p6Event) bool {
		return e.Type == "message.new" && e.Message.MessageID == sent.MessageID
	})
	if err != nil {
		return evt, err
	}
	msg := evt.Message
	if !hasEntityID(evt.ConvID, sent.ConvID) || msg.ConvID != sent.ConvID || !hasEntityID(msg.SenderID, sent.SenderID) || msg.Seq != sent.Seq || msg.Content.Text != sent.Content.Text {
		return evt, errors.New("p6.message.new: WS内容/身份/seq与REST确认不一致")
	}
	return evt, nil
}

func (d *driver) p6Presence(observer *p6Socket, user account, expected map[string]string) error {
	cursor := observer.cursor()
	if err := observer.send(map[string]string{"type": "presence.query", "user_id": user.id.String()}, d.timeout); err != nil {
		return err
	}
	evt, err := d.p6Wait(observer, cursor, "presence.query", func(e p6Event) bool { return e.Type == "presence.state" && hasEntityID(e.UserID, user.id) })
	if err != nil {
		return err
	}
	actual := make(map[string]string, len(evt.Devices))
	for _, device := range evt.Devices {
		if device.DeviceID == "" || device.InstanceID == "" || actual[device.DeviceID] != "" {
			return errors.New("p6.presence: 设备/实例为空或设备重复")
		}
		actual[device.DeviceID] = device.InstanceID
	}
	if evt.Online != (len(expected) != 0) || len(actual) != len(expected) {
		return fmt.Errorf("p6.presence: user=%s online=%t devices=%d，期望devices=%d", user.id, evt.Online, len(actual), len(expected))
	}
	for device, instance := range expected {
		if actual[device] != instance {
			return fmt.Errorf("p6.presence: device=%s instance=%s，期望=%s", device, actual[device], instance)
		}
	}
	return nil
}

func (d *driver) p6NoConversation(s *p6Socket, convID entityID, since int) error {
	// All permitted recipients have already received their matching delivery.
	// Keep observing an unrelated authenticated user for an additional window.
	time.Sleep(time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closedAt.IsZero() {
		return errors.New("p6.isolation: 非成员观察连接已关闭，不能证明未收到")
	}
	for _, evt := range s.events[since:] {
		if evt.Type != "error" && (hasEntityID(evt.ConvID, convID) || evt.Message.ConvID == convID) {
			return fmt.Errorf("p6.isolation: 非成员收到会话%s的%s事件", convID, evt.Type)
		}
	}
	return nil
}

func (d *driver) realtimeP6(addressA, addressB string) error {
	if err := d.scenario(addressA, addressB); err != nil {
		return err
	}
	if err := d.p6Delivery(addressA, addressB); err != nil {
		return err
	}
	if err := d.botRuntime(addressA, addressB); err != nil {
		return err
	}
	return d.p6Lifecycle(addressA, addressB)
}

func (d *driver) p6Delivery(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	a, err := d.register("p6_a", suffix)
	if err != nil {
		return err
	}
	b, err := d.register("p6_b", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("p6_outside", suffix)
	if err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", a.token, map[string]any{"type": "group", "group_name": "p6_" + suffix, "member_ids": []string{b.id.String()}}, &conv); err != nil {
		return err
	}
	if conv.ID == "" {
		return errors.New("p6.group: 无有效会话ID")
	}
	connA, err := d.p6Connect(addressA, a)
	if err != nil {
		return err
	}
	defer connA.close()
	connB, err := d.p6Connect(addressB, b)
	if err != nil {
		return err
	}
	defer connB.close()
	mirror := a
	mirror.device += "_other"
	connMirror, err := d.p6Connect(addressB, mirror)
	if err != nil {
		return err
	}
	defer connMirror.close()
	watch, err := d.p6Connect(addressA, outsider)
	if err != nil {
		return err
	}
	defer watch.close()
	if err := d.p6Presence(watch, a, map[string]string{a.device: "realtime-a", mirror.device: "realtime-b"}); err != nil {
		return err
	}
	// Keeping the sockets alive for longer than the configured six-second TTL
	// proves renewal through the public heartbeat, independently for each device.
	time.Sleep(8 * time.Second)
	if err := d.p6Presence(watch, a, map[string]string{a.device: "realtime-a", mirror.device: "realtime-b"}); err != nil {
		return err
	}
	first, err := d.sendMessage(a, conv.ID, "p6_group_"+suffix, 0)
	if err != nil {
		return err
	}
	for _, target := range []*p6Socket{connB, connMirror} {
		evt, err := d.p6Message(target, first)
		if err != nil {
			return err
		}
		caller := b
		if target == connMirror {
			caller = a
		}
		row, err := d.conversationList("p6.unread.envelope", caller, conv.ID, true)
		if err != nil {
			return err
		}
		if evt.UnreadCount == nil || *evt.UnreadCount != row.UnreadCount {
			return errors.New("p6.unread: WS信封与权威REST未读数不一致")
		}
	}
	if err := d.p6NoConversation(watch, conv.ID, 0); err != nil {
		return err
	}
	for _, typing := range []struct{ request, response string }{{"typing", "typing"}, {"typing.stop", "typing.stop"}} {
		cursor := connB.cursor()
		if err := connA.send(map[string]string{"type": typing.request, "conv_id": conv.ID.String()}, d.timeout); err != nil {
			return err
		}
		if _, err := d.p6Wait(connB, cursor, typing.response, func(e p6Event) bool {
			return e.Type == typing.response && hasEntityID(e.ConvID, conv.ID) && hasEntityID(e.UserID, a.id)
		}); err != nil {
			return err
		}
	}
	if err := d.p6NoConversation(watch, conv.ID, 0); err != nil {
		return err
	}
	// A non-member cannot manufacture a delivery intent by typing into the group.
	before := connB.cursor()
	if err := watch.send(map[string]string{"type": "typing", "conv_id": conv.ID.String()}, d.timeout); err != nil {
		return err
	}
	if _, err := d.p6Wait(watch, 0, "typing.authorization", func(e p6Event) bool { return e.Type == "error" }); err != nil {
		return err
	}
	time.Sleep(time.Second)
	connB.mu.Lock()
	for _, evt := range connB.events[before:] {
		if evt.Type == "typing" && hasEntityID(evt.UserID, outsider.id) {
			connB.mu.Unlock()
			return errors.New("p6.typing: 非成员伪造typing被转发")
		}
	}
	connB.mu.Unlock()
	readCursor := connA.cursor()
	if err := d.request(http.MethodPut, "/convs/"+conv.ID.String()+"/read", b.token, map[string]any{"seq": first.Seq}, nil); err != nil {
		return err
	}
	if _, err := d.p6Wait(connA, readCursor, "read_receipt", func(e p6Event) bool {
		return e.Type == "read_receipt" && hasEntityID(e.ConvID, conv.ID) && hasEntityID(e.UserID, b.id) && e.LastReadSeq == first.Seq
	}); err != nil {
		return err
	}
	if _, err := d.p6Wait(connB, 0, "unread_count", func(e p6Event) bool {
		return e.Type == "unread_count" && hasEntityID(e.ConvID, conv.ID) && e.UnreadCount != nil && *e.UnreadCount == 0
	}); err != nil {
		return err
	}
	if err := d.conversationUnread("p6.read", b, conv.ID, first.Seq, true); err != nil {
		return err
	}
	second, err := d.sendMessage(b, conv.ID, "p6_reverse_"+suffix, first.Seq)
	if err != nil {
		return err
	}
	if _, err := d.p6Message(connA, second); err != nil {
		return err
	}
	if _, err := d.p6Message(connMirror, second); err != nil {
		return err
	}
	for _, mutation := range []string{"edit", "recall", "delete", "delete_all"} {
		sent, err := d.sendMessage(a, conv.ID, "p6_"+mutation+"_"+suffix, second.Seq)
		if err != nil {
			return err
		}
		for _, target := range []*p6Socket{connB, connMirror} {
			if _, err := d.p6Message(target, sent); err != nil {
				return err
			}
		}
		path := "/messages/" + sent.MessageID.String()
		method, eventType := http.MethodPut, "message.edited"
		input := map[string]any{"text": "p6_edited_" + suffix}
		if mutation == "recall" {
			method, path, eventType, input = http.MethodPost, path+"/recall", "message.recalled", map[string]any{"conversation_id": conv.ID}
		}
		if mutation == "delete" || mutation == "delete_all" {
			method, eventType, input = http.MethodDelete, "message.deleted", map[string]any{"conversation_id": conv.ID, "delete_for_all": mutation == "delete_all"}
		}
		if err := d.request(method, path, a.token, input, nil); err != nil {
			return err
		}
		targets := []*p6Socket{connMirror}
		if mutation != "delete" {
			targets = append(targets, connB)
		}
		for _, target := range targets {
			if _, err := d.p6Wait(target, 0, mutation, func(e p6Event) bool {
				return e.Type == eventType && (hasEntityID(e.ConvID, conv.ID) || e.Message.ConvID == conv.ID) && (hasEntityID(e.MessageID, sent.MessageID) || e.Message.MessageID == sent.MessageID)
			}); err != nil {
				return err
			}
		}
		if mutation == "delete" {
			time.Sleep(time.Second)
			connB.mu.Lock()
			leaked := slices.ContainsFunc(connB.events, func(e p6Event) bool { return e.Type == "message.deleted" && e.Message.MessageID == sent.MessageID })
			connB.mu.Unlock()
			if leaked {
				return errors.New("p6.delete: 个人删除错误广播给其它成员")
			}
			if err := d.conversationMessage("p6.delete.other_member_visible", b, sent); err != nil {
				return err
			}
		}
		if mutation == "edit" {
			sent.Content.Text = "p6_edited_" + suffix
			if err := d.conversationMessage("p6.edited", b, sent); err != nil {
				return err
			}
		}
	}
	if err := d.p6NoConversation(watch, conv.ID, 0); err != nil {
		return err
	}
	connMirror.close()
	// The public disconnect contract is immediate, not a wait for TTL expiry.
	if err := waitP6Closed(connMirror, d.timeout); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if err := d.p6Presence(watch, a, map[string]string{a.device: "realtime-a"}); err != nil {
		return err
	}
	migrated, err := d.p6Connect(addressB, a)
	if err != nil {
		return err
	}
	defer migrated.close()
	connA.close()
	if err := waitP6Closed(connA, d.timeout); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if err := d.p6Presence(watch, a, map[string]string{a.device: "realtime-b"}); err != nil {
		return err
	}
	final, err := d.sendMessage(b, conv.ID, "p6_generation_"+suffix, second.Seq)
	if err != nil {
		return err
	}
	if _, err := d.p6Message(migrated, final); err != nil {
		return err
	}
	fmt.Println("P6 evidence: 多设备登记/TTL后心跳/断开/同设备跨实例重连后旧连接不删新路由；群聊、typing、变更、已读未读及发送者其它设备通过")
	return nil
}

func waitP6Closed(s *p6Socket, timeout time.Duration) error {
	select {
	case <-s.done:
		return nil
	case <-time.After(timeout):
		return errors.New("p6.lifecycle: 等待真实连接关闭超时")
	}
}

func (d *driver) p6Checkpoint(action string) error {
	path := filepath.Join(d.controlDir, action)
	if err := os.WriteFile(path+".request", []byte("ready\n"), 0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(max(2*time.Minute, d.timeout*20))
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".done"); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("p6.lifecycle: runner没有完成%s", action)
}

func (d *driver) p6Lifecycle(addressA, addressB string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	sender, err := d.register("p6_survivor", suffix)
	if err != nil {
		return err
	}
	victim, err := d.register("p6_victim", suffix)
	if err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", sender.token, map[string]any{"type": "single", "peer_user_id": victim.id.String()}, &conv); err != nil {
		return err
	}
	if conv.ID == "" {
		return errors.New("p6.lifecycle: 无有效会话ID")
	}
	survivor, err := d.p6Connect(addressA, sender)
	if err != nil {
		return err
	}
	defer survivor.close()
	dead, err := d.p6Connect(addressB, victim)
	if err != nil {
		return err
	}
	defer dead.close()
	confirmed, err := d.sendMessage(sender, conv.ID, "p6_before_crash_"+suffix, 0)
	if err != nil {
		return err
	}
	if _, err := d.p6Message(dead, confirmed); err != nil {
		return err
	}
	if err := d.p6Presence(survivor, victim, map[string]string{victim.device: "realtime-b"}); err != nil {
		return err
	}
	syncPosition, err := d.conversationCatchup("p6.before-crash.sync", victim, conv.ID, 0, confirmed)
	if err != nil {
		return err
	}
	if err := d.p6Checkpoint("kill-b"); err != nil {
		return err
	}
	if err := waitP6Closed(dead, d.timeout); err != nil {
		return err
	}
	if err := d.p6Presence(survivor, victim, nil); err != nil {
		return err
	}
	missed, err := d.sendMessage(sender, conv.ID, "p6_missed_after_ttl_"+suffix, confirmed.Seq)
	if err != nil {
		return err
	}
	if err := d.p6Checkpoint("restart-b"); err != nil {
		return err
	}
	restored, err := d.p6Connect(addressB, victim)
	if err != nil {
		return err
	}
	defer restored.close()
	syncPosition, err = d.conversationCatchup("p6.crash.position.catchup", victim, conv.ID, syncPosition, missed)
	if err != nil {
		return err
	}
	row, err := d.conversationList("p6.crash.unread", victim, conv.ID, true)
	if err != nil {
		return err
	}
	if row.UnreadCount != 2 {
		return fmt.Errorf("p6.crash: 补拉后权威未读数=%d，期望2", row.UnreadCount)
	}
	fresh, err := d.sendMessage(sender, conv.ID, "p6_restored_"+suffix, missed.Seq)
	if err != nil {
		return err
	}
	if _, err := d.p6Message(restored, fresh); err != nil {
		return err
	}
	syncPosition, err = d.conversationCatchup("p6.before-feedback.sync", victim, conv.ID, syncPosition, fresh)
	if err != nil {
		return err
	}
	fmt.Printf("P6 evidence: SIGKILL真实断连 → TTL后offline → 缺失消息%s(seq=%s)按用户位点补齐至position=%s → 恢复实例投递成功\n", missed.MessageID, missed.Seq, syncPosition)
	if err := d.p6Checkpoint("feedback-kill-b"); err != nil {
		return err
	}
	if err := waitP6Closed(restored, d.timeout); err != nil {
		return err
	}
	feedbackMessage, err := d.sendMessage(sender, conv.ID, "p6_negative_feedback_"+suffix, fresh.Seq)
	if err != nil {
		return err
	}
	// Two seconds is deliberately less than the six-second route TTL: this
	// proves active negative feedback rather than accepting natural expiry.
	feedback := *d
	feedback.timeout = 2 * time.Second
	if err := feedback.p6EventuallyPresence(survivor, victim, nil); err != nil {
		return fmt.Errorf("p6.negative.feedback: %w", err)
	}
	if err := d.p6Checkpoint("feedback-restart-b"); err != nil {
		return err
	}
	recovered, err := d.p6Connect(addressB, victim)
	if err != nil {
		return err
	}
	defer recovered.close()
	if _, err := d.conversationCatchup("p6.negative.feedback.catchup", victim, conv.ID, syncPosition, feedbackMessage); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, "/convs/"+conv.ID.String()+"/read", victim.token, map[string]any{"seq": feedbackMessage.Seq}, nil); err != nil {
		return err
	}
	if err := d.conversationUnread("p6.recovered.read", victim, conv.ID, feedbackMessage.Seq, true); err != nil {
		return err
	}
	fmt.Println("P6 evidence: SIGKILL后TTL内投递触发负反馈，2秒内route offline，缺失消息补拉且已读未读恢复")
	// One account's independent devices make staggered drain visible without
	// relying on internal session maps or a shutdown RPC.
	connections := []*p6Socket{recovered}
	for i := range 5 {
		device := victim
		device.device += fmt.Sprintf("_drain_%d", i)
		conn, err := d.p6Connect(addressB, device)
		if err != nil {
			return err
		}
		defer conn.close()
		connections = append(connections, conn)
	}
	if err := d.p6Checkpoint("drain-b"); err != nil {
		return err
	}
	readinessAt, err := d.p6ReadinessRejected(addressB)
	if err != nil {
		return err
	}
	if accepted, err := d.connect(addressB, victim); err == nil {
		accepted.Close()
		return errors.New("p6.drain: readiness503之后仍接受新WS")
	}
	var times []time.Time
	for i, conn := range connections {
		if err := waitP6Closed(conn, d.timeout); err != nil {
			return err
		}
		conn.mu.Lock()
		at, readErr := conn.closedAt, conn.readErr
		conn.mu.Unlock()
		var closed *websocket.CloseError
		if !errors.As(readErr, &closed) || closed.Code != websocket.CloseServiceRestart {
			return fmt.Errorf("p6.drain: device%d未收到可恢复的service restart关闭码", i)
		}
		if !at.After(readinessAt) {
			return errors.New("p6.drain: WS断连先于readiness503")
		}
		times = append(times, at)
		fmt.Printf("P6 drain evidence: device=%d closed_at=%s after_readiness_ms=%d\n", i, at.UTC().Format(time.RFC3339Nano), at.Sub(readinessAt).Milliseconds())
	}
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	if spread := times[len(times)-1].Sub(times[0]); spread < 3*time.Second {
		return fmt.Errorf("p6.drain: 6连接关闭跨度=%s，期望至少3s，拒绝同时断开", spread)
	}
	if err := d.p6Presence(survivor, victim, nil); err != nil {
		return err
	}
	if err := d.p6Checkpoint("drain-exited"); err != nil {
		return err
	}
	last, err := d.p6Connect(addressB, victim)
	if err != nil {
		return err
	}
	defer last.close()
	final, err := d.sendMessage(sender, conv.ID, "p6_after_drain_"+suffix, fresh.Seq)
	if err != nil {
		return err
	}
	if _, err := d.p6Message(last, final); err != nil {
		return err
	}
	return d.p6Presence(survivor, victim, map[string]string{victim.device: "realtime-b"})
}

func (d *driver) p6ReadinessRejected(address string) (time.Time, error) {
	u, err := endpoint(address, "ws", "wss")
	if err != nil {
		return time.Time{}, err
	}
	u.Scheme, u.Path = "http", "/readiness"
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			cancel()
			return time.Time{}, err
		}
		resp, err := d.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			cancel()
			if resp.StatusCode == http.StatusServiceUnavailable {
				at := time.Now()
				fmt.Printf("P6 readiness evidence: HTTP503 at=%s\n", at.UTC().Format(time.RFC3339Nano))
				return at, nil
			}
		} else {
			cancel()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Time{}, errors.New("p6.drain: 未实际观测到readiness HTTP503（连接失败不是摘流量证据）")
}

func (d *driver) p6BotStream(s *p6Socket, botID, convID, requestID entityID, reply sentMessage) error {
	done, err := d.p6Wait(s, 0, "bot.streaming.done", func(e p6Event) bool {
		return e.Type == "bot.streaming.done" && hasEntityID(e.ConvID, convID) && hasEntityID(e.BotID, botID) && hasEntityID(e.MessageID, reply.MessageID)
	})
	if err != nil {
		return err
	}
	if done.StreamID == "" {
		return errors.New("p6.bot.stream: done缺少stream_id")
	}
	var content string
	previous := int64(-1)
	first := true
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, evt := range s.events {
		if evt.Type != "bot.streaming.chunk" || evt.StreamID != done.StreamID {
			continue
		}
		if !hasEntityID(evt.ConvID, convID) || !hasEntityID(evt.BotID, botID) || evt.Seq <= previous {
			return errors.New("p6.bot.stream: chunk身份/顺序不一致")
		}
		if first && !hasEntityID(evt.ReplyToID, requestID) {
			return errors.New("p6.bot.stream: 首chunk回复消息ID不匹配")
		}
		first = false
		previous = evt.Seq
		content += evt.Content
	}
	if first || content != reply.Content.Text {
		return errors.New("p6.bot.stream: chunk拼接不等于provider精确回复（拒绝fallback）")
	}
	return nil
}

func (d *driver) p6EventuallyPresence(observer *p6Socket, user account, expected map[string]string) error {
	deadline := time.Now().Add(d.timeout)
	var last error
	for time.Now().Before(deadline) {
		bounded := *d
		bounded.timeout = time.Until(deadline)
		if last = bounded.p6Presence(observer, user, expected); last == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}
