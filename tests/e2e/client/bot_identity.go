package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Every assertion below observes a public REST/WS response; no DB credentials
// or service RPC can substitute for the Bot's own bearer/connection identity.
type identityBinding struct {
	BotID     entityID  `json:"bot_id"`
	BindingID entityID  `json:"binding_id"`
	OwnerType string    `json:"owner_type"`
	OwnerID   *entityID `json:"owner_id"`
	AddedBy   entityID  `json:"added_by"`
	Name      string    `json:"name"`
	Avatar    string    `json:"avatar"`
}

type identityOutput struct {
	message    storedMessage
	text       string
	file       entityID
	resultType string
	resultData string
}

func sameEntityID(a, b *entityID) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (d *driver) identityBinding(caller account, conv, bot entityID) (identityBinding, error) {
	var response struct {
		Bots []identityBinding `json:"bots"`
	}
	if err := d.request(http.MethodGet, "/convs/"+conv.String()+"/bots", caller.token, nil, &response); err != nil {
		return identityBinding{}, err
	}
	if len(response.Bots) != 1 || response.Bots[0].BotID != bot || response.Bots[0].BindingID == "" {
		return identityBinding{}, errors.New("bot-identity.binding: 独立Bot列表缺失唯一有效binding")
	}
	return response.Bots[0], nil
}

func (d *driver) identityHumans(caller account, conv entityID, humans ...entityID) error {
	if err := d.conversationMembers("bot-identity.human-only", caller, conv, humans...); err != nil {
		return err
	}
	row, err := d.conversationList("bot-identity.human-count", caller, conv, true)
	if err != nil {
		return err
	}
	if row.MemberCount != int32(len(humans)) {
		return fmt.Errorf("bot-identity.count: 人类member_count=%d，期望%d", row.MemberCount, len(humans))
	}
	return nil
}

func (d *driver) identityMessage(caller account, id entityID) (storedMessage, error) {
	var response struct {
		Message storedMessage `json:"message"`
	}
	err := d.request(http.MethodGet, "/messages/"+id.String(), caller.token, nil, &response)
	return response.Message, err
}

func checkIdentityOutput(actual storedMessage, expected identityOutput) error {
	m := expected.message
	if actual.MessageID != m.MessageID || actual.ConvID != m.ConvID || actual.Seq != m.Seq || actual.Type != m.Type || actual.SenderType != "bot" || !sameEntityID(actual.SenderID, m.SenderID) || actual.SenderName != m.SenderName || actual.SenderAvatar != m.SenderAvatar || !sameEntityID(actual.BindingID, m.BindingID) || !sameEntityID(actual.RequesterID, m.RequesterID) || actual.RawPayload != m.RawPayload || !sameEntityID(actual.ReplyToID, m.ReplyToID) {
		return errors.New("bot-identity.projection: Bot sender/binding/requester/content type/seq 在公开投影不一致")
	}
	switch actual.Type {
	case 1:
		if actual.Text.Text != expected.text {
			return errors.New("bot-identity.text: 正文不是请求的精确文本")
		}
	case 3:
		if actual.File == nil || actual.File.FileID != expected.file {
			return errors.New("bot-identity.file: 已确认文件实体未还原")
		}
	case 10:
		if actual.Result == nil || actual.Result.Type != expected.resultType || actual.Result.Data != expected.resultData {
			return errors.New("bot-identity.result: 类型或结果JSON未还原")
		}
	default:
		return errors.New("bot-identity.content: 输出仍依赖旧Bot9内容类型")
	}
	return nil
}

func (d *driver) identityObserve(socket *p6Socket, caller account, expected identityOutput) error {
	evt, err := d.p6Wait(socket, 0, "bot-identity.output", func(e p6Event) bool {
		return e.Type == "message.new" && e.Message.MessageID == expected.message.MessageID
	})
	if err != nil {
		return err
	}
	m, w := expected.message, evt.Message
	if !hasEntityID(evt.ConvID, m.ConvID) || w.ConvID != m.ConvID || w.Seq != m.Seq || w.MsgType != m.Type || w.SenderType != "bot" || !sameEntityID(w.SenderID, m.SenderID) || w.SenderName != m.SenderName || w.SenderAvatar != m.SenderAvatar || !sameEntityID(w.BindingID, m.BindingID) || !sameEntityID(w.RequesterID, m.RequesterID) || w.RawPayload != m.RawPayload || !sameEntityID(w.ReplyToID, m.ReplyToID) {
		return errors.New("bot-identity.ws: 实时身份/绑定/请求者与REST不同")
	}
	if m.Type == 1 && w.Content.Text != expected.text || m.Type == 3 && !hasEntityID(w.Content.FileID, expected.file) || m.Type == 10 && (w.Content.Type != expected.resultType || w.Content.Data != expected.resultData) {
		return errors.New("bot-identity.ws: 实时原类型content未还原")
	}
	actual, err := d.identityMessage(caller, m.MessageID)
	if err != nil {
		return err
	}
	if err := checkIdentityOutput(actual, expected); err != nil {
		return err
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err := d.request(http.MethodGet, "/convs/"+m.ConvID.String()+"/messages?cursor=0&limit=50", caller.token, nil, &history); err != nil {
		return err
	}
	for _, row := range history.Messages {
		if row.MessageID == m.MessageID {
			return checkIdentityOutput(row, expected)
		}
	}
	return errors.New("bot-identity.history: 已确认Bot输出未进入会话历史")
}

func (d *driver) identityBotSocket(address, token, device string) (*websocket.Conn, error) {
	u, err := endpoint(address, "ws", "wss")
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/bot"
	return d.connect(u.String(), account{token: token, device: device})
}

func (d *driver) identityBotExchange(conn *websocket.Conn, input any, reject bool) (entityID, error) {
	if err := conn.SetWriteDeadline(time.Now().Add(d.timeout)); err != nil {
		return "", err
	}
	if err := conn.WriteJSON(input); err != nil {
		return "", err
	}
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return "", err
	}
	for {
		var response struct {
			Type      string   `json:"type"`
			MessageID entityID `json:"message_id"`
		}
		if err := conn.ReadJSON(&response); err != nil {
			return "", err
		}
		if response.Type == "error" {
			if reject {
				return "", nil
			}
			return "", errors.New("bot-identity.bot-ws: 合法Bot输出被拒绝")
		}
		if response.Type == "message.sent" {
			if reject || response.MessageID == "" {
				return "", errors.New("bot-identity.bot-ws: 无权限输出成功或ACK无实体ID")
			}
			return response.MessageID, nil
		}
	}
}

func identityHistoryPath(conv, binding, grant entityID, cursor sequenceNumber) string {
	query := url.Values{"binding_id": {binding.String()}, "cursor": {cursor.String()}, "limit": {"30"}}
	if grant != "" {
		query.Set("history_grant_id", grant.String())
	}
	return "/bot-app/convs/" + conv.String() + "/messages?" + query.Encode()
}

func (d *driver) identityGrant(caller account, conv, bot, binding entityID, start, end sequenceNumber) (entityID, error) {
	var response struct {
		ID entityID `json:"grant_id"`
	}
	err := d.request(http.MethodPost, "/convs/"+conv.String()+"/bots/"+bot.String()+"/history-grants", caller.token, map[string]any{"binding_id": binding, "start_seq": start, "end_seq": end}, &response)
	if err == nil && response.ID == "" {
		err = errors.New("bot-identity.grant: 授权响应缺少grant UUID")
	}
	return response.ID, err
}

func (d *driver) identityHistory(token string, conv, binding, grant entityID, expected ...sentMessage) error {
	var response struct {
		Messages   []storedMessage `json:"messages"`
		Pagination struct {
			HasMore bool `json:"has_more"`
		} `json:"pagination"`
	}
	if err := d.request(http.MethodGet, identityHistoryPath(conv, binding, grant, 0), token, nil, &response); err != nil {
		return err
	}
	if response.Pagination.HasMore || len(response.Messages) != len(expected) {
		return errors.New("bot-identity.grant: Bot获得授权范围外历史或缺失获准消息")
	}
	for _, message := range response.Messages {
		i := slices.IndexFunc(expected, func(sent sentMessage) bool { return sent.MessageID == message.MessageID })
		if i < 0 {
			return errors.New("bot-identity.grant: 授权范围外消息泄露")
		}
		if err := checkStoredMessage("bot-identity.granted-history", message, expected[i]); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) identitySync(caller account, position sequenceNumber, expected []identityOutput) error {
	pending := make(map[entityID]identityOutput, len(expected))
	for _, output := range expected {
		pending[output.message.MessageID] = output
	}
	deadline := time.Now().Add(d.timeout)
	for len(pending) > 0 {
		if time.Now().After(deadline) {
			return errors.New("bot-identity.sync: Bot text/file/result未完整进入用户增量流")
		}
		page, err := d.inboxRead("bot-identity", caller, position, 50)
		if err != nil {
			return err
		}
		if page.RebuildRequired {
			return errors.New("bot-identity.sync: 已知用户位点意外重建")
		}
		for _, change := range page.Changes {
			if output, ok := pending[change.Message.MessageID]; ok {
				if change.Kind != "message.new" {
					return errors.New("bot-identity.sync: Bot输出没有复用普通新消息语义")
				}
				if err := checkIdentityOutput(change.Message, output); err != nil {
					return err
				}
				delete(pending, change.Message.MessageID)
			}
		}
		position = page.NextPosition
		if !page.HasMore {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}

func (d *driver) botIdentity(address, secondAddress string) (result error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("identity_owner", suffix)
	if err != nil {
		return err
	}
	inviter, err := d.register("identity_inviter", suffix)
	if err != nil {
		return err
	}
	requester, err := d.register("identity_requester", suffix)
	if err != nil {
		return err
	}
	var bot botView
	name := "identity-bot-" + suffix
	if err := d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": name, "avatar": d.provider + "/identity-avatar-" + suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "response_triggers": []string{"always"}}, &bot); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/bots/"+bot.ID.String(), owner.token, nil, nil))
	}()
	if bot.ID == "" || bot.OwnerType != "user" || !hasEntityID(bot.OwnerID, owner.id) {
		return errors.New("bot-identity.owner: Bot控制所有权不是独立owner")
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := d.request(http.MethodPost, "/bots/"+bot.ID.String()+"/token", owner.token, map[string]int{"ttl_seconds": 300}, &issued); err != nil {
		return err
	}
	if issued.Token == "" {
		return errors.New("bot-identity.token: 无Bot凭据")
	}
	botCaller := account{token: issued.Token}
	var convs [2]entityID
	var bindings [2]identityBinding
	var seeds [2]sentMessage
	for i := range convs {
		var response struct {
			ID entityID `json:"conversation_id"`
		}
		if err := d.request(http.MethodPost, "/convs", inviter.token, map[string]any{"type": "group", "group_name": fmt.Sprintf("identity-%s-%d", suffix, i), "member_ids": []entityID{requester.id}}, &response); err != nil {
			return err
		}
		convs[i] = response.ID
		defer func() {
			result = errors.Join(result, d.request(http.MethodDelete, "/convs/"+response.ID.String(), inviter.token, nil, nil))
		}()
		seeds[i], err = d.sendMessage(inviter, convs[i], "PRIVATE_PRE_BIND_HISTORY_"+suffix, 0)
		if err != nil {
			return err
		}
		if err := d.request(http.MethodPost, "/convs/"+convs[i].String()+"/bots", inviter.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
			return err
		}
		bindings[i], err = d.identityBinding(inviter, convs[i], bot.ID)
		if err != nil {
			return err
		}
		if bindings[i].OwnerType != "user" || !hasEntityID(bindings[i].OwnerID, owner.id) || bindings[i].AddedBy != inviter.id || bindings[i].Name != name || bindings[i].Avatar != bot.Avatar || bot.Avatar == "" {
			return errors.New("bot-identity.roles: owner/inviter/name/avatar属性失配")
		}
		if err := d.identityHumans(inviter, convs[i], inviter.id, requester.id); err != nil {
			return err
		}
		for _, path := range []string{"/convs/" + convs[i].String(), "/messages/" + seeds[i].MessageID.String(), "/convs/" + convs[i].String() + "/messages?cursor=0&limit=30"} {
			if err := d.conversationForbidden("bot-owner-no-read", http.MethodGet, path, owner, nil); err != nil {
				return err
			}
		}
		if _, err := d.conversationList("bot-owner-invisible", owner, convs[i], false); err != nil {
			return err
		}
	}
	if bindings[0].BindingID == bindings[1].BindingID {
		return errors.New("bot-identity.binding: 两群错误复用同一生命周期")
	}
	if err := d.conversationSyncHidden("bot-owner-no-sync", owner, convs[0]); err != nil {
		return err
	}
	if err := d.conversationSyncHidden("bot-owner-no-sync-second", owner, convs[1]); err != nil {
		return err
	}
	fmt.Printf("Bot identity PASS: bot=%s owner=%s inviter=%s requester=%s; group bindings=%s/%s; member_count=2/2, owner REST/history/sync拒读\n", bot.ID, owner.id, inviter.id, requester.id, bindings[0].BindingID, bindings[1].BindingID)

	conn, err := d.identityBotSocket(address, issued.Token, "identity-bot-"+suffix)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := d.ready(conn); err != nil {
		return err
	}
	humanSocket, err := d.p6Connect(secondAddress, requester)
	if err != nil {
		return err
	}
	defer humanSocket.close()
	initial, err := d.inboxRead("bot-identity.before-output", requester, 0, 50)
	if err != nil {
		return err
	}
	first, err := d.sendMessage(requester, convs[0], "CURRENT_REQUEST_ONLY_"+suffix, seeds[0].Seq)
	if err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return err
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("bot-identity.bot-request: %w", err)
		}
		var event struct {
			Type      string   `json:"type"`
			BindingID entityID `json:"binding_id"`
			Message   struct {
				ID      entityID        `json:"message_id"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Sender struct {
				ID *entityID `json:"sender_id"`
			} `json:"sender"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if event.Message.ID != first.MessageID {
			continue
		}
		var content struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(event.Message.Content, &content); err != nil {
			return err
		}
		if event.BindingID != bindings[0].BindingID || !hasEntityID(event.Sender.ID, requester.id) || content.Text != first.Content.Text || strings.Contains(string(raw), seeds[0].Content.Text) {
			return errors.New("bot-identity.request: 邀请者冒充请求者、binding丢失或默认泄漏群recent-history")
		}
		break
	}
	second, err := d.sendMessage(inviter, convs[0], "AUTHORIZED_SECOND_"+suffix, first.Seq)
	if err != nil {
		return err
	}
	grant, err := d.identityGrant(requester, convs[0], bot.ID, bindings[0].BindingID, first.Seq, second.Seq)
	if err != nil {
		return err
	}
	if err := d.identityHistory(issued.Token, convs[0], bindings[0].BindingID, grant, first, second); err != nil {
		return err
	}
	grantPath := "/convs/" + convs[0].String() + "/bots/" + bot.ID.String() + "/history-grants"
	for _, denied := range []struct {
		caller       account
		method, path string
		body         any
		status       int
	}{
		{botCaller, http.MethodGet, identityHistoryPath(convs[0], bindings[0].BindingID, "", 0), nil, http.StatusBadRequest},
		{botCaller, http.MethodGet, identityHistoryPath(convs[0], bindings[0].BindingID, grant, seeds[0].Seq), nil, http.StatusForbidden},
		{botCaller, http.MethodGet, identityHistoryPath(convs[1], bindings[1].BindingID, grant, 0), nil, http.StatusForbidden},
		{owner, http.MethodPost, grantPath, map[string]any{"binding_id": bindings[0].BindingID, "start_seq": first.Seq, "end_seq": second.Seq}, http.StatusForbidden},
		{requester, http.MethodPost, grantPath, map[string]any{"binding_id": bindings[0].BindingID, "start_seq": first.Seq, "end_seq": second.Seq + 10000}, http.StatusBadRequest},
	} {
		if err := d.runtimeReject(denied.method, denied.path, denied.caller, denied.body, denied.status); err != nil {
			return err
		}
	}
	if err := d.request(http.MethodDelete, "/messages/"+second.MessageID.String(), requester.token, map[string]bool{"delete_for_all": false}, nil); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodPost, grantPath, requester, map[string]any{"binding_id": bindings[0].BindingID, "start_seq": second.Seq, "end_seq": second.Seq}, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.identityHistory(issued.Token, convs[0], bindings[0].BindingID, grant, first); err != nil {
		return err
	}
	fmt.Println("Bot identity PASS: request-only WS内容；指定seq grant仅返回授权两条；无grant/越界cursor/跨群/非成员owner/超MaxSeq拒读；个人删除不可重新授权或复活")
	for _, raw := range []string{"not-json", "null", `{"kb_sources":[{"type":"rag","kb_name":"name","title":"title","content":"content","kb_id":7,"doc_id":"01960000-0000-7000-8000-000000000001","chunk_id":"01960000-0000-7000-8000-000000000002"}]}`} {
		input := map[string]any{"conversation_id": convs[0], "binding_id": bindings[0].BindingID, "type": 1, "text": "invalid metadata", "raw_payload": raw}
		if err := d.runtimeReject(http.MethodPost, "/bot-app/messages", botCaller, input, http.StatusBadRequest); err != nil {
			return err
		}
		ws := map[string]any{"type": "message.send", "conv_id": convs[0], "binding_id": bindings[0].BindingID, "msg_type": "text", "content": map[string]string{"text": "invalid metadata"}, "raw_payload": raw}
		if _, err := d.identityBotExchange(conn, ws, true); err != nil {
			return err
		}
	}
	// A rejected executor payload cannot strand the human's durable sync position.
	if _, err := d.inboxRead("bot-identity.metadata-rejected", requester, initial.NextPosition, 50); err != nil {
		return err
	}
	fmt.Println("Bot identity PASS: REST/WS非法元数据和数字知识来源拒写，用户同步继续可读")

	file, err := d.attachmentUpload(inviter, "identity-"+suffix+".txt", "text/plain", []byte("identity-public-"+suffix), 3)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/files/"+file.info.FileID.String(), inviter.token, nil, nil))
	}()
	var outputs []identityOutput
	for i, conv := range convs {
		for _, kind := range []int32{1, 3, 10} {
			raw := fmt.Sprintf(`{"origin":"identity","group":%d,"kind":%d}`, i, kind)
			input := map[string]any{"conversation_id": conv, "binding_id": bindings[i].BindingID, "requester_id": requester.id, "type": kind, "raw_payload": raw}
			expected := identityOutput{text: "机器人身份消息" + suffix + fmt.Sprint(i), file: file.info.FileID, resultType: "identity.result", resultData: fmt.Sprintf(`{"group":%d,"ok":true}`, i)}
			switch kind {
			case 1:
				input["text"] = expected.text
			case 3:
				input["file"] = map[string]any{"file_id": file.info.FileID}
			case 10:
				input["result"] = map[string]string{"type": expected.resultType, "data": expected.resultData}
			}
			var ack struct {
				ID  entityID       `json:"message_id"`
				Seq sequenceNumber `json:"seq"`
			}
			if err := d.request(http.MethodPost, "/bot-app/messages", issued.Token, input, &ack); err != nil {
				return err
			}
			if ack.ID == "" || ack.Seq <= 0 {
				return errors.New("bot-identity.rest: 输出没有持久消息UUID/seq")
			}
			expected.message = storedMessage{MessageID: ack.ID, ConvID: conv, Seq: ack.Seq, Type: kind, SenderID: &bot.ID, SenderType: "bot", SenderName: name, SenderAvatar: bot.Avatar, BindingID: &bindings[i].BindingID, RequesterID: &requester.id, RawPayload: raw}
			if err := d.identityObserve(humanSocket, requester, expected); err != nil {
				return err
			}
			outputs = append(outputs, expected)
		}
	}
	if err := d.identitySync(requester, initial.NextPosition, outputs); err != nil {
		return err
	}
	if err := d.identityReferences(requester, outputs[0], suffix); err != nil {
		return err
	}
	if err := d.identityWSOutputs(conn, humanSocket, requester, bot, bindings[0], convs[0], name, file.info.FileID, suffix); err != nil {
		return err
	}
	webhookSecret, err := d.identityWebhookOutputs(owner, requester, bot, bindings[0], convs[0], humanSocket, file.info.FileID, suffix)
	if err != nil {
		return err
	}
	fmt.Println("Bot identity PASS: 同Bot两群REST text=1/file=3/result=10与人类WS/history/byID/用户增量sync显式身份一致；Bot WS三种输出成功；search/引用识别Bot")

	body := map[string]any{"conversation_id": convs[0], "binding_id": bindings[0].BindingID, "type": 1, "text": "rejected-" + suffix}
	for _, change := range []struct {
		binding any
		status  int
	}{{nil, http.StatusBadRequest}, {bindings[1].BindingID, http.StatusForbidden}} {
		if change.binding == nil {
			delete(body, "binding_id")
		} else {
			body["binding_id"] = change.binding
		}
		if err := d.runtimeReject(http.MethodPost, "/bot-app/messages", botCaller, body, change.status); err != nil {
			return err
		}
	}
	if _, err := d.identityBotExchange(conn, map[string]any{"type": "message.send", "conv_id": convs[0], "msg_type": "text", "content": map[string]string{"text": "missing-binding"}}, true); err != nil {
		return err
	}
	body["binding_id"] = bindings[0].BindingID
	body["requester_id"] = owner.id
	if err := d.runtimeReject(http.MethodPost, "/bot-app/messages", botCaller, body, http.StatusForbidden); err != nil {
		return err
	}
	delete(body, "requester_id")
	before, err := d.conversationList("bot-identity.before-remove", inviter, convs[0], true)
	if err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, "/convs/"+convs[0].String()+"/bots/"+bot.ID.String(), inviter.token, nil, nil); err != nil {
		return err
	}
	if err := d.identityHumans(inviter, convs[0], inviter.id, requester.id); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodPost, "/bot-app/messages", botCaller, body, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, identityHistoryPath(convs[0], bindings[0].BindingID, grant, 0), botCaller, nil, http.StatusForbidden); err != nil {
		return err
	}
	if _, err := d.identityBotExchange(conn, map[string]any{"type": "message.send", "conv_id": convs[0], "binding_id": bindings[0].BindingID, "msg_type": "text", "content": map[string]string{"text": "stale-binding"}}, true); err != nil {
		return err
	}
	if _, err := d.identityWebhook(webhookSecret, map[string]any{"type": "message.send", "conversation_id": convs[0], "binding_id": bindings[0].BindingID, "msg_type": "text", "content": map[string]string{"text": "stale-webhook-binding"}}, true); err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/convs/"+convs[0].String()+"/bots", inviter.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
		return err
	}
	rejoined, err := d.identityBinding(inviter, convs[0], bot.ID)
	if err != nil {
		return err
	}
	if rejoined.BindingID == bindings[0].BindingID || rejoined.OwnerType != bindings[0].OwnerType || !sameEntityID(rejoined.OwnerID, bindings[0].OwnerID) {
		return errors.New("bot-identity.rejoin: 复用了旧binding或改变稳定Bot所有权")
	}
	if err := d.runtimeReject(http.MethodPost, "/bot-app/messages", botCaller, body, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, identityHistoryPath(convs[0], rejoined.BindingID, grant, 0), botCaller, nil, http.StatusForbidden); err != nil {
		return err
	}
	if _, err := d.identityWebhook(webhookSecret, map[string]any{"type": "message.send", "conversation_id": convs[0], "binding_id": bindings[0].BindingID, "msg_type": "text", "content": map[string]string{"text": "rejoined-old-webhook-binding"}}, true); err != nil {
		return err
	}
	if _, err := d.identityBotExchange(conn, map[string]any{"type": "message.send", "conv_id": convs[0], "binding_id": bindings[0].BindingID, "msg_type": "text", "content": map[string]string{"text": "rejoined-old-ws-binding"}}, true); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, identityHistoryPath(convs[0], rejoined.BindingID, "", 0), botCaller, nil, http.StatusBadRequest); err != nil {
		return err
	}
	freshGrant, err := d.identityGrant(inviter, convs[0], bot.ID, rejoined.BindingID, seeds[0].Seq, seeds[0].Seq)
	if err != nil {
		return err
	}
	if err := d.identityHistory(issued.Token, convs[0], rejoined.BindingID, freshGrant, seeds[0]); err != nil {
		return err
	}
	other, err := d.identityBinding(inviter, convs[1], bot.ID)
	if err != nil {
		return err
	}
	if other.BindingID != bindings[1].BindingID {
		return errors.New("bot-identity.rejoin: 第一群生命周期改变了第二群binding")
	}
	body["binding_id"] = rejoined.BindingID
	body["text"] = "fresh-lifecycle-" + suffix
	var fresh struct {
		ID  entityID       `json:"message_id"`
		Seq sequenceNumber `json:"seq"`
	}
	if err := d.request(http.MethodPost, "/bot-app/messages", issued.Token, body, &fresh); err != nil {
		return err
	}
	if fresh.ID == "" || fresh.Seq <= before.MaxSeq {
		return errors.New("bot-identity.rejoin: 新binding未复用普通会话seq")
	}
	expected := identityOutput{text: body["text"].(string), message: storedMessage{MessageID: fresh.ID, ConvID: convs[0], Seq: fresh.Seq, Type: 1, SenderID: &bot.ID, SenderType: "bot", SenderName: name, SenderAvatar: bot.Avatar, BindingID: &rejoined.BindingID}}
	if err := d.identityObserve(humanSocket, requester, expected); err != nil {
		return err
	}
	body["conversation_id"] = convs[1]
	body["binding_id"] = bindings[1].BindingID
	body["text"] = "other-binding-still-active-" + suffix
	var unaffected struct {
		ID  entityID       `json:"message_id"`
		Seq sequenceNumber `json:"seq"`
	}
	if err := d.request(http.MethodPost, "/bot-app/messages", issued.Token, body, &unaffected); err != nil {
		return err
	}
	if unaffected.ID == "" || unaffected.Seq <= outputs[5].message.Seq {
		return errors.New("bot-identity.independent: 第二群独立binding无法继续发言")
	}
	stillActive := identityOutput{text: body["text"].(string), message: storedMessage{MessageID: unaffected.ID, ConvID: convs[1], Seq: unaffected.Seq, Type: 1, SenderID: &bot.ID, SenderType: "bot", SenderName: name, SenderAvatar: bot.Avatar, BindingID: &bindings[1].BindingID}}
	if err := d.identityObserve(humanSocket, requester, stillActive); err != nil {
		return err
	}
	for _, conv := range convs {
		if err := d.identityHumans(inviter, conv, inviter.id, requester.id); err != nil {
			return err
		}
	}
	fmt.Printf("Bot identity PASS: missing/cross binding与非成员requester拒写；移出旧binding/旧grant失效；rejoin %s -> %s，新grant/新输出成功且第二群%s不变\n", bindings[0].BindingID, rejoined.BindingID, other.BindingID)
	return nil
}

func (d *driver) identityReferences(caller account, output identityOutput, suffix string) error {
	confirmed := sentMessage{MessageID: output.message.MessageID, ConvID: output.message.ConvID, SenderID: *output.message.SenderID, SenderType: "bot", SenderName: output.message.SenderName, SenderAvatar: output.message.SenderAvatar, BindingID: output.message.BindingID, RequesterID: output.message.RequesterID, RawPayload: output.message.RawPayload, Seq: output.message.Seq}
	confirmed.Content.Text = output.text
	if _, err := d.inboxSearchVisible(caller, confirmed); err != nil {
		return err
	}
	key, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	var reply sentMessage
	if err := d.request(http.MethodPost, "/messages/send", caller.token, map[string]any{"conversation_id": output.message.ConvID, "client_msg_id": key.String(), "reply_to_msg_id": output.message.MessageID, "content": map[string]string{"text": "机器人引用消息" + suffix}}, &reply); err != nil {
		return err
	}
	quoted, err := d.identityMessage(caller, reply.MessageID)
	if err != nil {
		return err
	}
	if quoted.ReplyTo == nil || quoted.ReplyTo.MessageID != output.message.MessageID || quoted.ReplyTo.SenderType != "bot" || !sameEntityID(quoted.ReplyTo.SenderID, output.message.SenderID) || quoted.ReplyTo.SenderName != output.message.SenderName || quoted.ReplyTo.Type != 1 {
		return errors.New("bot-identity.quote: 引用未识别原Bot发送者和Text类型")
	}
	searched, err := d.inboxSearchVisible(caller, reply)
	if err != nil {
		return err
	}
	if searched.ReplyTo == nil || searched.ReplyTo.SenderType != "bot" || !sameEntityID(searched.ReplyTo.SenderID, output.message.SenderID) {
		return errors.New("bot-identity.search: 搜索引用丢失Bot身份")
	}
	return nil
}

func (d *driver) identityWSOutputs(conn *websocket.Conn, socket *p6Socket, caller account, bot botView, binding identityBinding, conv entityID, name string, file entityID, suffix string) error {
	for _, kind := range []int32{1, 3, 10} {
		label := "text"
		expected := identityOutput{text: "ws-bot-" + suffix, file: file, resultType: "ws.result", resultData: `{"ok":true}`}
		content := map[string]any{"text": expected.text}
		switch kind {
		case 3:
			label = "file"
			content = map[string]any{"file_id": file}
		case 10:
			label = "result"
			content = map[string]any{"type": expected.resultType, "data": expected.resultData}
		}
		id, err := d.identityBotExchange(conn, map[string]any{"type": "message.send", "conv_id": conv, "binding_id": binding.BindingID, "requester_id": caller.id, "msg_type": label, "content": content}, false)
		if err != nil {
			return err
		}
		stored, err := d.identityMessage(caller, id)
		if err != nil {
			return err
		}
		expected.message = storedMessage{MessageID: id, ConvID: conv, Seq: stored.Seq, Type: kind, SenderID: &bot.ID, SenderType: "bot", SenderName: name, SenderAvatar: bot.Avatar, BindingID: &binding.BindingID, RequesterID: &caller.id}
		if err := d.identityObserve(socket, caller, expected); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) identityWebhook(secret string, input any, reject bool) (entityID, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(raw); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.gateway+"/api/v1/webhook", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIM-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-AIM-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("bot-identity.webhook: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	var envelope struct {
		Code *int `json:"code"`
		Data struct {
			ID entityID `json:"message_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return "", err
	}
	if reject {
		if (resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusForbidden) || envelope.Code == nil || *envelope.Code == 0 {
			return "", fmt.Errorf("bot-identity.webhook: 越权输出未被拒绝 HTTP%d", resp.StatusCode)
		}
		return "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code == nil || *envelope.Code != 0 || envelope.Data.ID == "" {
		return "", fmt.Errorf("bot-identity.webhook: 合法输出无成功消息ACK HTTP%d", resp.StatusCode)
	}
	return envelope.Data.ID, nil
}

func (d *driver) identityWebhookOutputs(owner, requester account, bot botView, binding identityBinding, conv entityID, socket *p6Socket, file entityID, suffix string) (string, error) {
	path := "/bots/" + bot.ID.String()
	if err := d.request(http.MethodPut, path, owner.token, map[string]string{"sub_type": "webhook", "conn_mode": "webhook", "callback_url": d.provider + "/external-webhook"}, nil); err != nil {
		return "", err
	}
	var secrets struct {
		Webhook string `json:"webhook_secret"`
	}
	if err := d.request(http.MethodPost, path+"/secret/rotate", owner.token, map[string]any{}, &secrets); err != nil {
		return "", err
	}
	if secrets.Webhook == "" {
		return "", errors.New("bot-identity.webhook: 没有签名凭据")
	}
	for _, kind := range []int32{1, 3, 10} {
		label := "text"
		expected := identityOutput{text: "webhook-bot-" + suffix, file: file, resultType: "webhook.result", resultData: `{"ok":true}`}
		content := map[string]any{"text": expected.text}
		switch kind {
		case 3:
			label = "file"
			content = map[string]any{"file_id": file}
		case 10:
			label = "result"
			content = map[string]any{"type": expected.resultType, "data": expected.resultData}
		}
		input := map[string]any{"type": "message.send", "conversation_id": conv, "binding_id": binding.BindingID, "requester_id": requester.id, "msg_type": label, "content": content}
		id, err := d.identityWebhook(secrets.Webhook, input, false)
		if err != nil {
			return "", err
		}
		stored, err := d.identityMessage(requester, id)
		if err != nil {
			return "", err
		}
		expected.message = storedMessage{MessageID: id, ConvID: conv, Seq: stored.Seq, Type: kind, SenderID: &bot.ID, SenderType: "bot", SenderName: bot.Name, SenderAvatar: bot.Avatar, BindingID: &binding.BindingID, RequesterID: &requester.id}
		if err := d.identityObserve(socket, requester, expected); err != nil {
			return "", err
		}
	}
	if _, err := d.identityWebhook(secrets.Webhook, map[string]any{"type": "message.send", "conversation_id": conv, "msg_type": "text", "content": map[string]string{"text": "no-webhook-binding"}}, true); err != nil {
		return "", err
	}
	fmt.Println("Bot identity PASS: HMAC签名公开webhook text/file/result与REST/WS身份一致；无binding拒绝")
	return secrets.Webhook, nil
}
