package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
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
	var found identityBinding
	for _, binding := range response.Bots {
		if binding.BotID != bot {
			continue
		}
		if found.BindingID != "" || binding.BindingID == "" {
			return identityBinding{}, errors.New("bot-identity.binding: 目标Bot缺失唯一有效binding")
		}
		found = binding
	}
	if found.BindingID == "" {
		return identityBinding{}, errors.New("bot-identity.binding: 目标Bot缺失唯一有效binding")
	}
	return found, nil
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
	if err := d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": name, "avatar": d.provider + "/identity-avatar-" + suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "response_triggers": []string{"mention"}, "capabilities": executorCapabilities}, &bot); err != nil {
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
	defer func(close func() error) {
		// Read-only transport cleanup does not affect the scenario verdict.
		_ = close()
	}(conn.Close)
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
	first, err := d.sendBotMention(requester, bot.ID, convs[0], "CURRENT_REQUEST_ONLY_"+suffix)
	if err != nil {
		return err
	}
	executor := &controlledExecutor{d: d, bot: bot.ID, conn: conn, token: issued.Token}
	request, err := executor.request("")
	if err != nil {
		return err
	}
	raw, err := json.Marshal(request.Inputs)
	if err != nil {
		return err
	}
	if request.Run.BindingID != bindings[0].BindingID || request.Run.RequesterID != requester.id || !hasEntityID(request.Run.SourceMessageID, first.MessageID) || request.Text() != first.Content.Text || strings.Contains(string(raw), seeds[0].Content.Text) {
		return errors.New("bot-identity.request: 邀请者冒充请求者、binding丢失或默认泄漏群recent-history")
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
	file, err := d.attachmentUpload(inviter, "identity-"+suffix+".txt", "text/plain", []byte("identity-public-"+suffix), 3)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/files/"+file.info.FileID.String(), inviter.token, nil, nil))
	}()
	private, err := d.attachmentUpload(inviter, "identity-private-"+suffix+".txt", "text/plain", []byte("private-resource-"+suffix), 1)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/files/"+private.info.FileID.String(), inviter.token, nil, nil))
	}()
	var outputs []identityOutput
	var firstPublication map[string]any
	for _, mode := range []string{"ws", "webhook"} {
		if mode == "webhook" {
			if err := d.request(http.MethodPut, "/bots/"+bot.ID.String(), owner.token, map[string]string{"sub_type": "webhook", "conn_mode": "webhook", "callback_url": d.provider + "/external-webhook"}, nil); err != nil {
				return err
			}
			executor, err = d.webhookExecutor(owner, bot.ID)
			if err != nil {
				return err
			}
		}
		for i, conv := range convs {
			fileGrant, err := d.resourceGrant(inviter, bot.ID, conv, "file", file.info.FileID, "conversation", true)
			if err != nil {
				return err
			}
			for _, kind := range []int32{1, 3, 10} {
				if mode != "ws" || i != 0 || kind != 1 {
					extra := map[string]any{}
					if kind == 3 {
						extra["resource_grant_ids"] = []entityID{fileGrant.ID}
					}
					accepted, err := d.interactionStart(requester, bot.ID, conv, "typed-result-"+mode+suffix, extra)
					if err != nil {
						return err
					}
					request, err = executor.request(accepted.ID)
					if err != nil {
						return err
					}
				}
				expected := identityOutput{text: executorText(request.Text()), file: file.info.FileID, resultType: "identity.result", resultData: fmt.Sprintf(`{"group":%d,"ok":true}`, i)}
				label := "text"
				content := map[string]any{"text": expected.text}
				switch kind {
				case 3:
					label = "file"
					content = map[string]any{"file_id": expected.file}
				case 10:
					label = "result"
					content = map[string]any{"type": expected.resultType, "data": expected.resultData}
				}
				publisher := *executor
				if mode == "ws" && i == 1 {
					publisher.rest = true
				}
				publication, err := publisher.complete(request, label, content, expected.text)
				if err != nil {
					return err
				}
				replay, err := publisher.update(publication, false, false)
				if err != nil {
					return err
				}
				changed := maps.Clone(publication)
				changed["result_text"] = "contradictory result"
				if _, err := publisher.update(changed, true, false); err != nil {
					return err
				}
				done, err := d.publishedRun(requester, request.Run.ID)
				if err != nil {
					return err
				}
				if replay.ID != done.ID || done.BindingID != bindings[i].BindingID || done.RequesterID != requester.id {
					return errors.New("bot-identity.run: 重试改变Run或权威身份")
				}
				stored, err := d.identityMessage(requester, *done.MessageID)
				if err != nil {
					return err
				}
				expected.message = storedMessage{MessageID: *done.MessageID, ConvID: conv, Seq: stored.Seq, Type: kind, SenderID: &bot.ID, SenderType: "bot", SenderName: name, SenderAvatar: bot.Avatar, BindingID: &bindings[i].BindingID, RequesterID: &requester.id, RawPayload: stored.RawPayload, ReplyToID: request.Run.SourceMessageID}
				if err := d.identityObserve(humanSocket, requester, expected); err != nil {
					return err
				}
				outputs = append(outputs, expected)
				if mode == "webhook" && i == 0 && kind == 1 {
					firstPublication = publication
				}
			}
		}
		// A rejected private result must never enter the public Run or its receipts.
		denied, err := d.startRun(requester, bot.ID, convs[0], "private-file-"+mode+suffix, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		deniedRequest, err := executor.request(denied.ID)
		if err != nil {
			return err
		}
		for _, payload := range []map[string]any{executorPayload(deniedRequest, "run.claim", "accepted", 0), executorPayload(deniedRequest, "run.update", "running", 1)} {
			if _, err := executor.update(payload, false, false); err != nil {
				return err
			}
		}
		privateResult := executorPayload(deniedRequest, "run.update", "completed", 2)
		privateResult["msg_type"], privateResult["content"], privateResult["result_text"] = "file", map[string]any{"file_id": private.info.FileID, "name": "PRIVATE_UNSHAREABLE_NAME", "url": "https://private.invalid/secret"}, "PRIVATE_UNSHAREABLE_SUMMARY"
		if _, err := executor.update(privateResult, true, false); err != nil {
			return err
		}
		unchanged, err := d.readRun(requester, denied.ID)
		if err != nil {
			return err
		}
		if unchanged.Status != "running" || unchanged.OutputStatus != "none" || unchanged.MessageID != nil || unchanged.ResultText != "" || len(unchanged.ResultContent) != 0 {
			return errors.New("bot-identity.file: 私有文件或伪造元信息进入公开执行结果")
		}
	}
	if err := d.identitySync(requester, initial.NextPosition, outputs); err != nil {
		return err
	}
	if err := d.identityReferences(requester, outputs[0], suffix); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, identityHistoryPath(convs[0], bindings[0].BindingID, grant, 0), botCaller, nil, http.StatusUnauthorized); err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/bots/"+bot.ID.String()+"/token", owner.token, map[string]int{"ttl_seconds": 300}, &issued); err != nil {
		return err
	}
	botCaller.token = issued.Token
	if err := d.request(http.MethodDelete, "/convs/"+convs[0].String()+"/bots/"+bot.ID.String(), inviter.token, nil, nil); err != nil {
		return err
	}
	if _, err := executor.update(firstPublication, true, false); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, identityHistoryPath(convs[0], bindings[0].BindingID, grant, 0), botCaller, nil, http.StatusForbidden); err != nil {
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
	if _, err := executor.update(firstPublication, true, false); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, identityHistoryPath(convs[0], rejoined.BindingID, grant, 0), botCaller, nil, http.StatusForbidden); err != nil {
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
		return errors.New("bot-identity.rejoin: 第一群生命周期改变第二群binding")
	}
	for i, conv := range convs {
		accepted, err := d.startRun(requester, bot.ID, conv, "fresh-lifecycle-"+suffix, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		freshRequest, err := executor.request(accepted.ID)
		if err != nil {
			return err
		}
		text := executorText(freshRequest.Text())
		if _, err := executor.complete(freshRequest, "text", map[string]any{"text": text}, text); err != nil {
			return err
		}
		done, err := d.publishedRun(requester, accepted.ID)
		if err != nil {
			return err
		}
		expectedBinding := bindings[i].BindingID
		if i == 0 {
			expectedBinding = rejoined.BindingID
		}
		if done.BindingID != expectedBinding {
			return errors.New("bot-identity.rejoin: 新Run未绑定当前生命周期")
		}
		stored, err := d.identityMessage(requester, *done.MessageID)
		if err != nil {
			return err
		}
		if stored.Seq <= outputs[0].message.Seq {
			return errors.New("bot-identity.rejoin: 会话seq发生回退")
		}
		expected := identityOutput{text: text, message: storedMessage{MessageID: *done.MessageID, ConvID: conv, Seq: stored.Seq, Type: 1, SenderID: &bot.ID, SenderType: "bot", SenderName: name, SenderAvatar: bot.Avatar, BindingID: &expectedBinding, RequesterID: &requester.id, RawPayload: stored.RawPayload}}
		if err := d.identityObserve(humanSocket, requester, expected); err != nil {
			return err
		}
		if err := d.identityHumans(inviter, conv, inviter.id, requester.id); err != nil {
			return err
		}
	}
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
