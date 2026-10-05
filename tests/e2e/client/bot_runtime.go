package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

type providerObservations struct {
	MCPLists     int      `json:"mcp_lists"`
	ToolTexts    []string `json:"tool_texts"`
	ChatModels   []string `json:"chat_models"`
	EmbedModels  []string `json:"embed_models"`
	ActiveIngest int      `json:"active_ingest"`
}

func (d *driver) observeProvider() (providerObservations, error) {
	var observed providerObservations
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.provider+"/observations", nil)
	if err != nil {
		return observed, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return observed, fmt.Errorf("provider observations: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return observed, errors.New("provider observations HTTP 非200")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&observed); err != nil {
		return observed, errors.New("provider observations JSON 无效")
	}
	return observed, nil
}

func (d *driver) fixtureModel(owner account, capability, name string) (decimal, error) {
	var created struct {
		ID      decimal `json:"id"`
		OwnerID decimal `json:"owner_id"`
		Name    string  `json:"model_name"`
		BaseURL string  `json:"base_url"`
	}
	baseURL := d.provider + "/v1"
	if err := d.request(http.MethodPost, "/models", owner.token, map[string]any{
		"model_name": name, "provider": "openai", "capability": capability,
		"base_url": baseURL, "api_key": "e2e-fixture-not-a-secret",
	}, &created); err != nil {
		return 0, fmt.Errorf("provider.model.registry: %w", err)
	}
	if created.ID <= 0 || created.OwnerID != owner.id || created.Name != name || created.BaseURL != baseURL {
		return 0, errors.New("provider.model.registry: gateway注册模型身份或provider URL不匹配")
	}
	return created.ID, nil
}

type botView struct {
	ID       decimal  `json:"id"`
	OwnerID  decimal  `json:"owner_id"`
	Name     string   `json:"name"`
	ModelID  decimal  `json:"model_id"`
	Prompt   string   `json:"system_prompt"`
	Callback string   `json:"callback_url"`
	Triggers []string `json:"response_triggers"`
}

func (d *driver) botRuntime(address, secondAddress string) (result error) {
	crossInstance := address != secondAddress
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("bot", suffix)
	if err != nil {
		return err
	}
	modelName := "fixture-chat-" + suffix
	modelID, err := d.fixtureModel(owner, "chat", modelName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+modelID.String(), owner.token, nil, nil))
	}()
	before, err := d.observeProvider()
	if err != nil {
		return err
	}
	var bot botView
	if err := d.request(http.MethodPost, "/bots", owner.token, map[string]any{
		"name": "p5-bot-" + suffix, "type": "self_deployed", "model_id": modelID,
		"model_name": modelName, "system_prompt": "Use fixture_echo to echo the user's exact text before replying.",
		"response_triggers": []string{"always"}, "streaming_enabled": crossInstance, "enable_knowledge": false,
	}, &bot); err != nil {
		return err
	}
	if bot.ID <= 0 || bot.OwnerID != owner.id || bot.ModelID != modelID {
		return errors.New("bot.create: ID/owner/model不匹配")
	}
	path := "/bots/" + bot.ID.String()
	deleted := false
	defer func() {
		if !deleted {
			result = errors.Join(result, d.request(http.MethodDelete, path, owner.token, nil, nil))
		}
	}()
	prompt := "P5 updated prompt: use fixture_echo before replying"
	if err := d.request(http.MethodPut, path, owner.token, map[string]any{
		"name": "p5-configured-" + suffix, "model_id": modelID, "system_prompt": prompt,
		"response_triggers": []string{"always"}, "streaming_enabled": crossInstance, "enable_knowledge": false,
		"max_context_messages": 4, "temperature": 0.2,
	}, nil); err != nil {
		return err
	}
	var fetched botView
	if err := d.request(http.MethodGet, path, owner.token, nil, &fetched); err != nil {
		return err
	}
	if fetched.ID != bot.ID || fetched.Name != "p5-configured-"+suffix || fetched.Prompt != prompt || fetched.ModelID != modelID || !slices.Equal(fetched.Triggers, []string{"always"}) {
		return errors.New("bot.config: GET没有返回刚持久化的配置")
	}
	var listed struct {
		Bots []botView `json:"bots"`
	}
	if err := d.request(http.MethodGet, "/bots", owner.token, nil, &listed); err != nil {
		return err
	}
	if !slices.ContainsFunc(listed.Bots, func(b botView) bool { return b.ID == bot.ID && b.Name == fetched.Name }) {
		return errors.New("bot.list: 配置后的Bot不可见")
	}
	var issued struct {
		Token     string  `json:"token"`
		ExpiresAt decimal `json:"expires_at"`
	}
	if err := d.request(http.MethodPost, path+"/token", owner.token, map[string]int{"ttl_seconds": 300}, &issued); err != nil {
		return err
	}
	if issued.Token == "" || issued.ExpiresAt <= decimal(time.Now().Unix()) {
		return errors.New("bot.token: 未签发有效期限token")
	}
	var validated struct {
		Valid   bool    `json:"valid"`
		BotID   decimal `json:"bot_id"`
		OwnerID decimal `json:"owner_id"`
	}
	if err := d.request(http.MethodPost, "/bots/token/validate", owner.token, map[string]string{"token": issued.Token}, &validated); err != nil {
		return err
	}
	if !validated.Valid || validated.BotID != bot.ID || validated.OwnerID != owner.id {
		return errors.New("bot.token: 验证身份不匹配")
	}
	var invalid struct {
		Valid bool `json:"valid"`
	}
	if err := d.request(http.MethodPost, "/bots/token/validate", owner.token, map[string]string{"token": "invalid.fixture.token"}, &invalid); err != nil {
		var rejected *apiError
		if !errors.As(err, &rejected) {
			return err
		}
	} else if invalid.Valid {
		return errors.New("bot.token: 非法token被接受")
	}

	var mcp struct {
		ID        decimal `json:"id"`
		URL       string  `json:"url"`
		Transport string  `json:"transport"`
	}
	if err := d.request(http.MethodPost, "/mcp-servers", owner.token, map[string]any{
		"name": "fixture-mcp-" + suffix, "transport": "sse", "url": d.provider + "/mcp/sse", "enabled": true,
		"advanced_config": `{"timeout":10,"retry_count":0}`,
	}, &mcp); err != nil {
		return err
	}
	if mcp.ID <= 0 || mcp.URL != d.provider+"/mcp/sse" || mcp.Transport != "sse" {
		return errors.New("bot.mcp: 网络server配置不匹配")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/mcp-servers/"+mcp.ID.String(), owner.token, nil, nil))
	}()
	type toolView struct {
		Name     string  `json:"name"`
		ServerID decimal `json:"mcp_server_id"`
		Schema   string  `json:"input_schema"`
	}
	for _, operation := range []struct{ method, path string }{
		{http.MethodPost, "/mcp-servers/" + mcp.ID.String() + "/discover"},
		{http.MethodGet, "/mcp-servers/" + mcp.ID.String() + "/tools"},
	} {
		var tools struct {
			Tools []toolView `json:"tools"`
		}
		if err := d.request(operation.method, operation.path, owner.token, nil, &tools); err != nil {
			return err
		}
		if !slices.ContainsFunc(tools.Tools, func(t toolView) bool {
			var schema struct {
				Type     string   `json:"type"`
				Required []string `json:"required"`
			}
			return t.Name == "fixture_echo" && t.ServerID == mcp.ID && json.Unmarshal([]byte(t.Schema), &schema) == nil && schema.Type == "object" && slices.Contains(schema.Required, "text")
		}) {
			return errors.New("bot.mcp: 网络发现和持久化工具schema不匹配")
		}
	}
	if err := d.request(http.MethodPost, path+"/mcp-servers", owner.token, map[string]any{"mcp_server_id": mcp.ID}, nil); err != nil {
		return err
	}
	var conv struct {
		ID decimal `json:"conversation_id"`
	}
	var remote, mirror, observer *p6Socket
	var member, outsider account
	if crossInstance {
		member, err = d.register("bot_member", suffix)
		if err != nil {
			return err
		}
		outsider, err = d.register("bot_outside", suffix)
		if err != nil {
			return err
		}
	}
	if err := d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "group", "group_name": "p5-bot-" + suffix}, &conv); err != nil {
		return err
	}
	if conv.ID <= 0 {
		return errors.New("bot.conversation: 无有效会话ID")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/convs/"+conv.ID.String(), owner.token, nil, nil))
	}()
	if crossInstance {
		if err := d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/members/invite", owner.token, map[string]any{"user_ids": []int64{int64(member.id)}}, nil); err != nil {
			return err
		}
		remote, err = d.p6Connect(secondAddress, member)
		if err != nil {
			return err
		}
		defer remote.close()
		otherDevice := owner
		otherDevice.device += "_bot_mirror"
		mirror, err = d.p6Connect(secondAddress, otherDevice)
		if err != nil {
			return err
		}
		defer mirror.close()
		observer, err = d.p6Connect(secondAddress, outsider)
		if err != nil {
			return err
		}
		defer observer.close()
	}
	if err := d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", owner.token, map[string]any{"bot_id": bot.ID.String()}, nil); err != nil {
		return err
	}
	conn, err := d.connect(address, owner)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := d.ready(conn); err != nil {
		return err
	}
	text := "P5_BOT_REQUEST_" + suffix
	sent, err := d.sendMessage(owner, conv.ID, text, 0)
	if err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return err
	}
	var reply sentMessage
	for {
		evt, err := readEvent(conn)
		if err != nil {
			return fmt.Errorf("bot.kafka.reply: 等待真实Bot回复: %w", err)
		}
		if evt.Type == "error" {
			return errors.New("bot.reply: WS error")
		}
		if evt.Type != "message.new" || evt.Message.SenderID != bot.ID || evt.ConvID != conv.ID || evt.Message.ReplyToID != sent.MessageID {
			continue
		}
		if evt.Message.Content.Text != "fixture-reply:"+text || evt.Message.MessageID <= 0 || evt.Message.Seq <= sent.Seq || evt.Message.ConvID != conv.ID {
			return errors.New("bot.reply: Bot回复不是精确provider结果（拒绝fallback），或消息标识/seq无效")
		}
		reply.MessageID, reply.ConvID, reply.SenderID, reply.Seq = evt.Message.MessageID, conv.ID, bot.ID, evt.Message.Seq
		reply.Content.Text = evt.Message.Content.Text
		break
	}
	if err := d.conversationMessage("bot.reply.read", owner, reply); err != nil {
		return err
	}
	if err := d.conversationSync("bot.reply.sync", owner, conv.ID, sent.Seq, reply); err != nil {
		return err
	}
	if crossInstance {
		for _, target := range []*p6Socket{remote, mirror} {
			if _, err := d.p6Message(target, reply); err != nil {
				return err
			}
			if err := d.p6BotStream(target, bot.ID, conv.ID, sent.MessageID, reply); err != nil {
				return err
			}
		}
		if err := d.p6NoConversation(observer, conv.ID, 0); err != nil {
			return err
		}
		fmt.Println("P6 evidence: Bot真实provider/MCP回复和流式chunk/done同路由跨实例/发送者另一设备，非成员无事件")
	}
	after, err := d.observeProvider()
	if err != nil {
		return err
	}
	if after.MCPLists <= before.MCPLists || !slices.Contains(after.ChatModels, modelName) || !slices.Contains(after.ToolTexts, text) {
		return errors.New("bot.provider: 未观测到真实HTTP MCP发现、模型请求及exact文本工具调用")
	}
	if err := d.request(http.MethodDelete, path, owner.token, nil, nil); err != nil {
		return err
	}
	deleted = true
	var remaining struct {
		Bots []botView `json:"bots"`
	}
	if err := d.request(http.MethodGet, "/bots", owner.token, nil, &remaining); err != nil {
		return err
	}
	if slices.ContainsFunc(remaining.Bots, func(b botView) bool { return b.ID == bot.ID }) {
		return errors.New("bot.delete: 删除后列表仍存在")
	}
	return d.webhookConfig(owner, suffix)
}

func (d *driver) webhookConfig(owner account, suffix string) (result error) {
	var created botView
	callback := d.provider + "/external-webhook"
	if err := d.request(http.MethodPost, "/bots", owner.token, map[string]any{
		"name": "p5-webhook-" + suffix, "type": "third_party", "sub_type": "webhook", "conn_mode": "webhook", "callback_url": callback,
	}, &created); err != nil {
		return err
	}
	if created.ID <= 0 || created.Callback != callback {
		return errors.New("bot.webhook: 创建callback配置未持久化")
	}
	path := "/bots/" + created.ID.String()
	defer func() { result = errors.Join(result, d.request(http.MethodDelete, path, owner.token, nil, nil)) }()
	callback += "?configured=1"
	if err := d.request(http.MethodPut, path, owner.token, map[string]string{"callback_url": callback, "sub_type": "webhook", "conn_mode": "webhook"}, nil); err != nil {
		return err
	}
	var fetched botView
	if err := d.request(http.MethodGet, path, owner.token, nil, &fetched); err != nil {
		return err
	}
	if fetched.ID != created.ID || fetched.Callback != callback {
		return errors.New("bot.webhook: 更新callback配置未持久化")
	}
	var secrets struct {
		Webhook string `json:"webhook_secret"`
		App     string `json:"app_secret"`
	}
	if err := d.request(http.MethodPost, path+"/secret/rotate", owner.token, map[string]any{}, &secrets); err != nil {
		return err
	}
	if secrets.Webhook == "" || secrets.App == "" {
		return errors.New("bot.webhook: secret轮换未签发两种secret")
	}
	return nil
}
