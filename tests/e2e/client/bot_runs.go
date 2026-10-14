package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Run observations come only from authenticated REST/WS and the external provider.
type publicRun struct {
	ID              entityID              `json:"id"`
	InteractionID   entityID              `json:"interaction_id"`
	BotID           entityID              `json:"bot_id"`
	ConvID          entityID              `json:"conv_id"`
	BindingID       entityID              `json:"binding_id"`
	RequesterID     entityID              `json:"requester_id"`
	Visibility      string                `json:"visibility"`
	PersonalUserID  *entityID             `json:"personal_user_id"`
	Actions         []authorizationAction `json:"actions"`
	ControllerID    entityID              `json:"controller_id"`
	ControlUserIDs  []entityID            `json:"control_user_ids"`
	StopStatus      string                `json:"stop_status"`
	StreamStatus    string                `json:"stream_status"`
	StopReason      string                `json:"stop_reason"`
	SourceMessageID *entityID             `json:"source_message_id"`
	Version         int64                 `json:"version"`
	Status          string                `json:"status"`
	Stage           string                `json:"stage"`
	ResultText      string                `json:"result_text"`
	ResultContent   json.RawMessage       `json:"result_content"`
	OutputStatus    string                `json:"output_status"`
	MessageID       *entityID             `json:"message_id"`
	Error           string                `json:"error"`
	Generation      int64                 `json:"generation"`
	InputVersion    int64                 `json:"input_version"`
	InputSources    []struct {
		Version         int64     `json:"version"`
		RequesterID     entityID  `json:"requester_id"`
		SourceMessageID *entityID `json:"source_message_id"`
	} `json:"input_sources"`
	TriggerKind           string               `json:"trigger_kind"`
	Origin                *collaborationOrigin `json:"origin"`
	DelegationParentRunID *entityID            `json:"delegation_parent_run_id"`
	ChildRunIDs           []entityID           `json:"child_run_ids"`
	DelegationDepth       int64                `json:"delegation_depth"`
	Activity              *struct {
		MessageID entityID `json:"message_id"`
		ID        string   `json:"id"`
		Type      string   `json:"type"`
	} `json:"activity"`
	ContextNotice        string `json:"context_notice"`
	ExecutorType         string `json:"executor_type"`
	DeliveryStatus       string `json:"delivery_status"`
	ExecutorStatus       string `json:"executor_status"`
	ExecutorLeaseUntil   *int64 `json:"executor_lease_until"`
	ExecutorCapabilities struct {
		InputTypes     []string `json:"input_types"`
		SupportsInput  bool     `json:"supports_input"`
		SupportsStop   bool     `json:"supports_stop"`
		SupportsResume bool     `json:"supports_resume"`
	} `json:"executor_capabilities"`
	Sources []runtimeKnowledgeSource `json:"sources"`
	Steps   []struct {
		Stage string `json:"stage"`
	} `json:"steps"`
}

func (d *driver) startRun(caller account, bot, conv entityID, text, key string, reply *entityID) (publicRun, error) {
	var run publicRun
	body := map[string]any{"conv_id": conv, "submission_key": key, "message": text}
	if reply != nil {
		body["reply_to_msg_id"] = *reply
	}
	err := d.request(http.MethodPost, "/bots/"+bot.String()+"/runs", caller.token, body, &run)
	return run, err
}

func (d *driver) waitRun(caller account, id entityID) (publicRun, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		var run publicRun
		if err := d.request(http.MethodGet, "/runs/"+id.String(), caller.token, nil, &run); err != nil {
			return run, err
		}
		if run.Status == "succeeded" || run.Status == "failed" || run.Status == "stopped" {
			return run, nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return publicRun{}, errors.New("bot-runs: Run未在截止前结束")
}

// Exercise ordinary IM mutations without treating them as work controls.
func (d *driver) mutateWorkSource(sender, viewer account, source sentMessage, operation string) error {
	path := "/messages/" + source.MessageID.String()
	switch operation {
	case "edit":
		edited := "EDITED_NOT_ACCEPTED_" + source.MessageID.String()
		if err := d.request(http.MethodPut, path, sender.token, map[string]string{"text": edited}, nil); err != nil {
			return err
		}
		current, err := d.identityMessage(viewer, source.MessageID)
		if err != nil {
			return err
		}
		if current.Text.Text != edited || current.EditCount != 1 {
			return errors.New("work-source: ordinary IM did not expose the edit")
		}
	case "personal_delete":
		if err := d.request(http.MethodDelete, path, viewer.token, map[string]bool{"delete_for_all": false}, nil); err != nil {
			return err
		}
		if err := d.inboxMessageAbsent("accepted-work-personal-source", viewer, source.MessageID); err != nil {
			return err
		}
		if sender.id != viewer.id {
			current, err := d.identityMessage(sender, source.MessageID)
			if err != nil {
				return err
			}
			if current.Text.Text != source.Content.Text {
				return errors.New("work-source: personal deletion changed another account's message")
			}
		}
	case "recall":
		if err := d.request(http.MethodPost, path+"/recall", sender.token, map[string]any{}, nil); err != nil {
			return err
		}
		current, err := d.identityMessage(viewer, source.MessageID)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		if current.Status != 2 || strings.Contains(string(raw), source.Content.Text) {
			return errors.New("work-source: recalled message exposed original body")
		}
	case "delete_all":
		if err := d.request(http.MethodDelete, path, sender.token, map[string]bool{"delete_for_all": true}, nil); err != nil {
			return err
		}
		for _, caller := range []account{sender, viewer} {
			if err := d.inboxMessageAbsent("accepted-work-global-source", caller, source.MessageID); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("work-source: unknown operation %s", operation)
	}
	return nil
}

func (d *driver) sendBotMention(caller account, bot, conv entityID, text string) (sentMessage, error) {
	var sent sentMessage
	key := uuid.NewString()
	err := d.request(http.MethodPost, "/messages/send", caller.token, map[string]any{"conversation_id": conv, "client_msg_id": key, "content": map[string]any{"text": text, "mentions": []entityID{bot}}}, &sent)
	return sent, err
}

func (d *driver) conversationRuns(caller account, conv entityID) ([]publicRun, error) {
	var runs []publicRun
	err := d.request(http.MethodGet, "/conversations/"+conv.String()+"/runs", caller.token, nil, &runs)
	return runs, err
}

func (d *driver) botRuns(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("run_owner", suffix)
	if err != nil {
		return err
	}
	member, err := d.register("run_member", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("run_outsider", suffix)
	if err != nil {
		return err
	}
	modelID, err := d.fixtureModel(owner, "chat", "fixture-run-"+suffix)
	if err != nil {
		return err
	}
	var mcp struct {
		ID entityID `json:"id"`
	}
	if err = d.request(http.MethodPost, "/mcp-servers", owner.token, map[string]any{"name": "run-tools-" + suffix, "transport": "sse", "url": d.provider + "/mcp/sse", "enabled": true, "advanced_config": `{"timeout":10,"retry_count":0}`}, &mcp); err != nil {
		return err
	}
	var bots [2]botView
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "group", "group_name": "runs-" + suffix, "member_ids": []entityID{member.id}}, &conv); err != nil {
		return err
	}
	if err = d.fixtureEchoPolicy(owner, mcp.ID, conv.ID); err != nil {
		return err
	}
	for i := range bots {
		if err = d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": fmt.Sprintf("run-%s-%d", suffix, i), "type": "self_deployed", "model_id": modelID, "system_prompt": "Use fixture_echo to echo the exact user text.", "response_triggers": []string{"mention"}, "streaming_enabled": i == 0, "enable_knowledge": false}, &bots[i]); err != nil {
			return err
		}
		if err = d.request(http.MethodPost, "/bots/"+bots[i].ID.String()+"/mcp-servers", owner.token, map[string]any{"mcp_server_id": mcp.ID}, nil); err != nil {
			return err
		}
		if err = d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", owner.token, map[string]any{"bot_id": bots[i].ID}, nil); err != nil {
			return err
		}
	}
	socket, err := d.p6Connect(secondAddress, member)
	if err != nil {
		return err
	}
	defer socket.close()
	seed, err := d.sendMessage(owner, conv.ID, "PRIVATE_UNRELATED_HISTORY_"+suffix, 0)
	if err != nil {
		return err
	}
	text := "RUN_EXPLICIT_" + suffix
	key := uuid.NewString()
	accepted, err := d.startRun(member, bots[0].ID, conv.ID, text, key, nil)
	if err != nil {
		return err
	}
	if accepted.ID == "" || accepted.InteractionID == "" || accepted.ID == accepted.InteractionID || accepted.RequesterID != member.id || accepted.BotID != bots[0].ID || accepted.ConvID != conv.ID {
		return errors.New("bot-runs.accept: 独立身份或请求者缺失")
	}
	var results [4]publicRun
	var failures [4]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i], failures[i] = d.startRun(member, bots[0].ID, conv.ID, text, key, nil) })
	}
	wg.Wait()
	for i, duplicate := range results {
		if failures[i] != nil {
			return failures[i]
		}
		if duplicate.ID != accepted.ID {
			return errors.New("bot-runs.retry: 同键创建了多个Run")
		}
	}
	if _, err = d.startRun(member, bots[0].ID, conv.ID, text+"changed", key, nil); err == nil {
		return errors.New("bot-runs.conflict: 同键不同内容未冲突")
	} else {
		var api *apiError
		if !errors.As(err, &api) || api.status != http.StatusConflict {
			return fmt.Errorf("bot-runs.conflict: 未返回HTTP409: %w", err)
		}
	}
	done, err := d.waitRun(member, accepted.ID)
	if err != nil {
		return err
	}
	if done.Status != "succeeded" || done.OutputStatus != "committed" || done.MessageID == nil || done.ResultText != "fixture-reply:"+text {
		return errors.New("bot-runs.result: 结果/消息提交状态不正确")
	}
	if *done.MessageID == done.ID || *done.MessageID == done.InteractionID {
		return errors.New("bot-runs.identity: Run被冒充消息ID")
	}
	if _, err = d.p6Wait(socket, 0, "run-status", func(e p6Event) bool {
		return e.Type == "bot.run.updated" && e.Run.ID == done.ID && e.Run.Status == "succeeded" && e.Run.MessageID != nil && *e.Run.MessageID == *done.MessageID
	}); err != nil {
		return err
	}
	msg, err := d.identityMessage(member, *done.MessageID)
	if err != nil {
		return err
	}
	if msg.SenderType != "bot" || !hasEntityID(msg.SenderID, bots[0].ID) || !hasEntityID(msg.RequesterID, member.id) || msg.Text.Text != done.ResultText {
		return errors.New("bot-runs.history: 真实持久消息身份失配")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = d.request(http.MethodGet, "/convs/"+conv.ID.String()+"/messages?limit=50", member.token, nil, &history); err != nil {
		return err
	}
	botMessages := 0
	for _, row := range history.Messages {
		if row.SenderType == "bot" {
			botMessages++
			if row.MessageID != *done.MessageID {
				return errors.New("bot-runs.history: 重复或临时输出进入聊天历史")
			}
		}
	}
	if botMessages != 1 {
		return errors.New("bot-runs.history: 最终Bot消息不唯一")
	}
	if err = d.conversationForbidden("run-read", http.MethodGet, "/runs/"+done.ID.String(), outsider, nil); err != nil {
		return err
	}
	socket.close()
	socket, err = d.p6Connect(address, member)
	if err != nil {
		return err
	}
	defer socket.close()
	restored, err := d.conversationRuns(member, conv.ID)
	if err != nil {
		return err
	}
	if len(restored) != 1 || restored[0].ID != done.ID || restored[0].MessageID == nil || *restored[0].MessageID != *done.MessageID {
		return errors.New("bot-runs.reconnect: 权威查询无法恢复Run和最终消息")
	}
	mentionText := "RUN_MENTION_" + suffix
	mention, err := d.sendBotMention(member, bots[1].ID, conv.ID, mentionText)
	if err != nil {
		return err
	}
	var mentioned publicRun
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		runs, e := d.conversationRuns(member, conv.ID)
		if e != nil {
			return e
		}
		for _, run := range runs {
			if hasEntityID(run.SourceMessageID, mention.MessageID) {
				mentioned = run
			}
		}
		if mentioned.ID != "" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if mentioned.ID == "" || mentioned.BotID != bots[1].ID || mentioned.RequesterID != member.id || mentioned.InteractionID == done.InteractionID {
		return errors.New("bot-runs.mention: 未触发目标独立Run")
	}
	if mentioned, err = d.waitRun(member, mentioned.ID); err != nil {
		return err
	}
	if mentioned.MessageID == nil {
		return errors.New("bot-runs.mention: 无最终消息")
	}
	mentionedMessage, err := d.identityMessage(member, *mentioned.MessageID)
	if err != nil {
		return err
	}
	if !hasEntityID(mentionedMessage.ReplyToID, mention.MessageID) {
		return errors.New("bot-runs.mention: 最终回复未关联发起请求消息")
	}
	runs, err := d.conversationRuns(member, conv.ID)
	if err != nil {
		return err
	}
	if len(runs) != 2 {
		return errors.New("bot-runs.trigger: 普通群消息/其他mention/Bot输出启动了额外Run")
	}
	for i := range bots {
		if err = d.botRunMixedTools(member, bots[i].ID, conv.ID, socket, fmt.Sprintf("RUN_MIXED_WAIT_%s_%d", suffix, i)); err != nil {
			return err
		}
	}
	budgetToken := "RUN_BUDGET_WAIT_" + suffix
	budget, err := d.startRun(member, bots[0].ID, conv.ID, budgetToken, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	// The five-call cumulative budget survives four input interrupts and reserves
	// its final call for delivery, without resetting on Runner resume.
	for round := range 4 {
		if _, err = d.interactionWait(member, budget.ID, "waiting_input"); err != nil {
			return err
		}
		if _, err = d.interactionControl(member, budget.ID, "input", fmt.Sprintf("BUDGET_CONTINUE_%d", round), uuid.NewString(), nil); err != nil {
			return err
		}
	}
	budget, err = d.waitRun(member, budget.ID)
	if err != nil {
		return err
	}
	var boundedAnswer struct {
		Unfinished bool `json:"unfinished"`
	}
	if budget.Status != "succeeded" || budget.MessageID == nil || json.Unmarshal([]byte(budget.ResultText), &boundedAnswer) != nil || !boundedAnswer.Unfinished {
		return errors.New("bot-runs.budget: 恢复未按累计预算交付明确未完成范围")
	}
	budgetObservation, err := d.observeProvider()
	if err != nil {
		return err
	}
	budgetCalls := 0
	for _, request := range budgetObservation.ChatRequests {
		encoded, _ := json.Marshal(request["messages"])
		if strings.Contains(string(encoded), budgetToken) {
			budgetCalls++
		}
	}
	if budgetCalls != 5 {
		return fmt.Errorf("bot-runs.budget: 真实供应商调用越过累计预算 calls=%d", budgetCalls)
	}
	failed, err := d.startRun(member, bots[0].ID, conv.ID, "RUN_FAIL_PROVIDER_"+suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	failed, err = d.waitRun(member, failed.ID)
	if err != nil {
		return err
	}
	if failed.Status != "failed" || failed.MessageID != nil || failed.Error == "" {
		return errors.New("bot-runs.failure: 模型失败被伪装已发送")
	}
	denied, err := d.startRun(member, bots[0].ID, conv.ID, "RUN_WRITE_DENY_"+suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	denied, err = d.waitRun(member, denied.ID)
	if err != nil {
		return err
	}
	if denied.Status != "failed" || denied.MessageID != nil {
		return errors.New("bot-runs.tools: 未授权外部写未明确拒绝")
	}
	heldText := "RUN_HOLD_PROVIDER_CONFIG_" + suffix
	held, err := d.startRun(member, bots[0].ID, conv.ID, heldText, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodPut, "/bots/"+bots[0].ID.String(), owner.token, map[string]any{"system_prompt": "NEW_CONFIG_ONLY_FOR_NEXT_RUN"}, nil); err != nil {
		return err
	}
	if err = d.request(http.MethodPut, "/models/"+modelID.String(), owner.token, map[string]any{"model_name": "fixture-next-" + suffix}, nil); err != nil {
		return err
	}
	held, err = d.waitRun(member, held.ID)
	if err != nil {
		return err
	}
	if held.Status != "succeeded" || held.MessageID == nil {
		return errors.New("bot-runs.freeze: 当前Run因普通配置更新失效")
	}
	revokedText := "RUN_HOLD_PROVIDER_REVOKE_" + suffix
	revokeKey := uuid.NewString()
	revoked, err := d.startRun(member, bots[1].ID, conv.ID, revokedText, revokeKey, nil)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodDelete, "/convs/"+conv.ID.String()+"/bots/"+bots[1].ID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	revoked, err = d.waitRun(member, revoked.ID)
	if err != nil {
		return err
	}
	if revoked.Status != "stopped" || revoked.StopStatus != "confirmed" || revoked.MessageID != nil {
		return errors.New("bot-runs.revoke: 移出后仍提交最终消息")
	}
	if _, err = d.startRun(member, bots[1].ID, conv.ID, revokedText, revokeKey, nil); err == nil {
		return errors.New("bot-runs.revoke: 幂等重试绕过当前资格")
	}
	observed, err := d.observeProvider()
	if err != nil {
		return err
	}
	var requests []map[string]json.RawMessage
	for _, request := range observed.ChatRequests {
		var model string
		_ = json.Unmarshal(request["model"], &model)
		if model == "fixture-run-"+suffix {
			requests = append(requests, request)
		}
	}
	if len(requests) < 4 {
		return errors.New("bot-runs.provider: 未观测真实模型工具往返")
	}
	first, second := requests[0], requests[1]
	var firstMessages, secondMessages []map[string]json.RawMessage
	if json.Unmarshal(first["messages"], &firstMessages) != nil || json.Unmarshal(second["messages"], &secondMessages) != nil {
		return errors.New("bot-runs.provider: 请求内容非法")
	}
	if len(secondMessages) != len(firstMessages)+2 {
		return errors.New("bot-runs.trajectory: 工具往返未合法追加")
	}
	for i, block := range firstMessages {
		a, _ := json.Marshal(block)
		b, _ := json.Marshal(secondMessages[i])
		if string(a) != string(b) {
			return errors.New("bot-runs.prefix: 旧模型块被重写")
		}
	}
	if string(first["tools"]) != string(second["tools"]) {
		return errors.New("bot-runs.prefix: 工具定义漂移")
	}
	encoded, _ := json.Marshal(firstMessages)
	if strings.Contains(string(encoded), seed.Content.Text) {
		return errors.New("bot-runs.isolation: 未授权群消息进入模型")
	}
	if slicesContains := strings.Join(observed.ToolTexts, "\n"); strings.Contains(slicesContains, "RUN_WRITE_DENY_"+suffix) {
		return errors.New("bot-runs.tools: 未授权外部写进入真实网络工具")
	}
	for _, request := range observed.ChatRequests {
		raw, _ := json.Marshal(request["messages"])
		if strings.Contains(string(raw), heldText) {
			var model string
			_ = json.Unmarshal(request["model"], &model)
			if model != "fixture-run-"+suffix || strings.Contains(string(raw), "NEW_CONFIG_ONLY_FOR_NEXT_RUN") {
				return errors.New("bot-runs.freeze: 执行中模型/提示词被偷偷改变")
			}
		}
	}
	fmt.Printf("Run evidence PASS: run=%s interaction=%s message=%s; explicit/mention, duplicate/conflict, real MCP, stable append requests, mixed tool/input rounds with no replay in stream and sync, failure, WS and reconnect/history\n", done.ID, done.InteractionID, *done.MessageID)
	return nil
}

// A model round spans real network work on both sides of an input interrupt.
// Recovering it must retain complete pairing and not repeat completed network work.
func (d *driver) botRunMixedTools(caller account, bot, conv entityID, socket *p6Socket, token string) error {
	accepted, err := d.startRun(caller, bot, conv, token, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	waiting, err := d.interactionWait(caller, accepted.ID, "waiting_input")
	if err != nil {
		return err
	}
	if waiting.WaitingReason == "" || waiting.OutputStatus != "none" || waiting.MessageID != nil || waiting.ResultText != "" {
		return errors.New("bot-runs.mixed-wait: 输入中断被伪装为空结果成功")
	}
	if _, err = d.p6Wait(socket, 0, "mixed-wait", func(e p6Event) bool {
		return e.Type == "bot.run.updated" && e.Run.ID == waiting.ID && e.Run.Status == "waiting_input"
	}); err != nil {
		return err
	}
	checkTools := func(resumed bool) error {
		observed, err := d.observeProvider()
		if err != nil {
			return err
		}
		before, after := 0, 0
		for _, text := range observed.ToolTexts {
			switch text {
			case token + ":before":
				before++
			case token + ":after":
				after++
			}
		}
		if before != 1 || after > 1 || resumed && after != 1 {
			return fmt.Errorf("bot-runs.mixed-tools: MCP执行遗漏或重复 before=%d after=%d resumed=%t", before, after, resumed)
		}
		return nil
	}
	if err = checkTools(false); err != nil {
		return err
	}
	supplement := "CONTINUE_MIXED_" + token
	receipt, err := d.interactionControl(caller, waiting.ID, "input", supplement, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if receipt.ID != waiting.ID || receipt.InputVersion != waiting.InputVersion+1 {
		return errors.New("bot-runs.mixed-input: 恢复未接受同Run的新输入版本")
	}
	done, err := d.interactionWait(caller, waiting.ID, "succeeded")
	if err != nil {
		return err
	}
	if done.MessageID == nil || done.ResultText != "fixture-resumed:"+token+"|"+supplement || done.InputVersion != receipt.InputVersion {
		return errors.New("bot-runs.mixed-result: 恢复丢失原输入、补充或真实最终结果")
	}
	if err = checkTools(true); err != nil {
		return err
	}
	if _, err = d.p6Wait(socket, 0, "mixed-resumed", func(e p6Event) bool {
		return e.Type == "bot.run.updated" && e.Run.ID == done.ID && e.Run.Status == "succeeded" && e.Run.MessageID != nil && *e.Run.MessageID == *done.MessageID
	}); err != nil {
		return err
	}
	message, err := d.identityMessage(caller, *done.MessageID)
	if err != nil {
		return err
	}
	if message.Text.Text != done.ResultText || message.SenderType != "bot" || !hasEntityID(message.SenderID, bot) || !hasEntityID(message.RequesterID, caller.id) {
		return errors.New("bot-runs.mixed-history: 最终消息身份或内容失配")
	}
	return nil
}
