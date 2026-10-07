package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	llmpb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type providerObservations struct {
	MCPLists     int      `json:"mcp_lists"`
	ToolTexts    []string `json:"tool_texts"`
	ChatModels   []string `json:"chat_models"`
	EmbedModels  []string `json:"embed_models"`
	RerankModels []string `json:"rerank_models"`
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

func (d *driver) fixtureModel(owner account, capability, name string) (entityID, error) {
	var created struct {
		OwnerType string    `json:"owner_type"`
		ID        entityID  `json:"id"`
		OwnerID   *entityID `json:"owner_id"`
		Name      string    `json:"model_name"`
		BaseURL   string    `json:"base_url"`
	}
	baseURL := d.provider + "/v1"
	if err := d.request(http.MethodPost, "/models", owner.token, map[string]any{
		"model_name": name, "provider": "openai", "capability": capability,
		"base_url": baseURL, "api_key": "e2e-fixture-not-a-secret",
		"input_price_per_mtok": 2, "output_price_per_mtok": 3,
	}, &created); err != nil {
		return "", fmt.Errorf("provider.model.registry: %w", err)
	}
	if created.ID == "" || created.OwnerType != "user" || !hasEntityID(created.OwnerID, owner.id) || created.Name != name || created.BaseURL != baseURL {
		return "", errors.New("provider.model.registry: gateway注册模型身份或provider URL不匹配")
	}
	return created.ID, nil
}

type botView struct {
	OwnerType              string    `json:"owner_type"`
	ID                     entityID  `json:"id"`
	OwnerID                *entityID `json:"owner_id"`
	Name                   string    `json:"name"`
	ModelID                *entityID `json:"model_id"`
	Prompt                 string    `json:"system_prompt"`
	Callback               string    `json:"callback_url"`
	Triggers               []string  `json:"response_triggers"`
	MemoryModelID          *entityID `json:"memory_model_id"`
	MemoryEmbeddingModelID *entityID `json:"memory_embedding_model_id"`
	Streaming              bool      `json:"streaming_enabled"`
	Status                 string    `json:"status"`
	Temperature            float64   `json:"temperature"`
	MaxContext             int32     `json:"max_context_messages"`
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
	outsider, err := d.register("bot_outside", suffix)
	if err != nil {
		return err
	}
	modelName := "fixture-chat-" + suffix
	modelID, err := d.fixtureModel(owner, "chat", modelName+"-initial")
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+modelID.String(), owner.token, nil, nil))
	}()
	if err := d.runtimeModelCRUD(owner, outsider, modelID, modelName); err != nil {
		return err
	}
	toolsModelID, err := d.fixtureModel(owner, "chat", "fixture-tools-"+suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+toolsModelID.String(), owner.token, nil, nil))
	}()
	embeddingID, err := d.fixtureModel(owner, "embed", "fixture-embed-"+suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+embeddingID.String(), owner.token, nil, nil))
	}()
	foreignID, err := d.fixtureModel(outsider, "chat", "fixture-foreign-"+suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+foreignID.String(), outsider.token, nil, nil))
	}()
	if err := d.runtimeCapabilities(owner, outsider, embeddingID, suffix); err != nil {
		return err
	}
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
	if bot.ID == "" || bot.OwnerType != "user" || !hasEntityID(bot.OwnerID, owner.id) || !hasEntityID(bot.ModelID, modelID) {
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
	if fetched.ID != bot.ID || fetched.Name != "p5-configured-"+suffix || fetched.Prompt != prompt || !hasEntityID(fetched.ModelID, modelID) || !slices.Equal(fetched.Triggers, []string{"always"}) {
		return errors.New("bot.config: GET没有返回刚持久化的配置")
	}
	if !fetched.Streaming && crossInstance || fetched.Streaming && !crossInstance || fetched.Temperature != 0.2 || fetched.MaxContext != 4 {
		return errors.New("bot.config: 数字配置或streaming没有持久化")
	}
	if err := d.runtimeModelReferences(owner, outsider, path, bot.ID, modelID, toolsModelID, embeddingID, foreignID, crossInstance); err != nil {
		return err
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
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at,string"`
	}
	if err := d.request(http.MethodPost, path+"/token", owner.token, map[string]int{"ttl_seconds": 300}, &issued); err != nil {
		return err
	}
	if issued.Token == "" || issued.ExpiresAt <= time.Now().Unix() {
		return errors.New("bot.token: 未签发有效期限token")
	}
	var validated struct {
		Valid   bool      `json:"valid"`
		BotID   entityID  `json:"bot_id"`
		OwnerID *entityID `json:"owner_id"`
	}
	if err := d.request(http.MethodPost, "/bots/token/validate", owner.token, map[string]string{"token": issued.Token}, &validated); err != nil {
		return err
	}
	if !validated.Valid || validated.BotID != bot.ID || !hasEntityID(validated.OwnerID, owner.id) {
		return errors.New("bot.token: 验证身份不匹配")
	}
	if err := d.runtimeReject(http.MethodPost, path+"/token", outsider, map[string]int{"ttl_seconds": 300}, http.StatusForbidden); err != nil {
		return err
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
		ID        entityID `json:"id"`
		URL       string   `json:"url"`
		Transport string   `json:"transport"`
	}
	if err := d.request(http.MethodPost, "/mcp-servers", owner.token, map[string]any{
		"name": "fixture-mcp-" + suffix, "transport": "sse", "url": d.provider + "/mcp/sse", "enabled": true,
		"advanced_config": `{"timeout":10,"retry_count":0}`,
	}, &mcp); err != nil {
		return err
	}
	if mcp.ID == "" || mcp.URL != d.provider+"/mcp/sse" || mcp.Transport != "sse" {
		return errors.New("bot.mcp: 网络server配置不匹配")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/mcp-servers/"+mcp.ID.String(), owner.token, nil, nil))
	}()
	type toolView struct {
		ID       entityID `json:"id"`
		Name     string   `json:"name"`
		ServerID entityID `json:"mcp_server_id"`
		Schema   string   `json:"input_schema"`
	}
	var toolID entityID
	for _, operation := range []struct{ method, path string }{
		{http.MethodPost, "/mcp-servers/" + mcp.ID.String() + "/discover"},
		{http.MethodPost, "/mcp-servers/" + mcp.ID.String() + "/discover"},
		{http.MethodGet, "/mcp-servers/" + mcp.ID.String() + "/tools"},
	} {
		var tools struct {
			Tools []toolView `json:"tools"`
		}
		if err := d.request(operation.method, operation.path, owner.token, nil, &tools); err != nil {
			return err
		}
		if len(tools.Tools) != 1 {
			return errors.New("bot.mcp: 重复发现必须只有一个fixture工具")
		}
		t := tools.Tools[0]
		var schema struct {
			Type     string   `json:"type"`
			Required []string `json:"required"`
		}
		if t.ID == "" || t.Name != "fixture_echo" || t.ServerID != mcp.ID || json.Unmarshal([]byte(t.Schema), &schema) != nil || schema.Type != "object" || !slices.Contains(schema.Required, "text") {
			return errors.New("bot.mcp: 网络发现和持久化工具UUID/schema不匹配")
		}
		if toolID != "" && toolID != t.ID {
			return errors.New("bot.mcp: 重复发现改变工具UUID")
		}
		toolID = t.ID
	}
	if err := d.request(http.MethodPost, path+"/mcp-servers", owner.token, map[string]any{"mcp_server_id": mcp.ID}, nil); err != nil {
		return err
	}
	if err := d.runtimeMCPBindings(owner, outsider, path, mcp.ID); err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	var remote, mirror, observer *p6Socket
	var member account
	if crossInstance {
		member, err = d.register("bot_member", suffix)
		if err != nil {
			return err
		}
	}
	if err := d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "group", "group_name": "p5-bot-" + suffix}, &conv); err != nil {
		return err
	}
	if conv.ID == "" {
		return errors.New("bot.conversation: 无有效会话ID")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/convs/"+conv.ID.String(), owner.token, nil, nil))
	}()
	if crossInstance {
		if err := d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/members/invite", owner.token, map[string]any{"user_ids": []entityID{member.id}}, nil); err != nil {
			return err
		}
		remote, err = d.p6Connect(secondAddress, member)
		if err != nil {
			return err
		}
		defer remote.close()
		otherDevice, err := d.inboxLoginDevice(owner, "bot_mirror")
		if err != nil {
			return err
		}
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
		if evt.Type != "message.new" || !hasEntityID(evt.Message.SenderID, bot.ID) || !hasEntityID(evt.ConvID, conv.ID) || !hasEntityID(evt.Message.ReplyToID, sent.MessageID) {
			continue
		}
		if evt.Message.Content.Text != "fixture-reply:"+text || evt.Message.MessageID == "" || evt.Message.Seq <= sent.Seq || evt.Message.ConvID != conv.ID {
			return errors.New("bot.reply: Bot回复不是精确provider结果（拒绝fallback），或消息标识/seq无效")
		}
		reply.MessageID, reply.ConvID, reply.SenderID, reply.Seq = evt.Message.MessageID, conv.ID, bot.ID, evt.Message.Seq
		reply.Content.Text = evt.Message.Content.Text
		break
	}
	if err := d.conversationMessage("bot.reply.read", owner, reply); err != nil {
		return err
	}
	if err := d.conversationSync("bot.reply.sync", owner, conv.ID, reply); err != nil {
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
	if err := d.runtimeBilling(owner, outsider, bot.ID, modelID, modelName, 2); err != nil {
		return err
	}
	if err := d.runtimeConversationTools(owner, outsider, address, secondAddress, conv.ID, sent, toolsModelID); err != nil {
		return err
	}
	if err := d.runtimePrivateBotConversation(owner, outsider, secondAddress, bot.ID, suffix); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]any{"status": "disabled"}, nil); err != nil {
		return err
	}
	if err := d.runtimeTokenState(owner, issued.Token, bot.ID, false); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]any{"status": "active"}, nil); err != nil {
		return err
	}
	if err := d.runtimeTokenState(owner, issued.Token, bot.ID, true); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, path, owner.token, nil, nil); err != nil {
		return err
	}
	deleted = true
	if err := d.runtimeTokenState(owner, issued.Token, bot.ID, false); err != nil {
		return err
	}
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
	if created.ID == "" || created.OwnerType != "user" || !hasEntityID(created.OwnerID, owner.id) || created.ModelID != nil || created.Callback != callback {
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

type runtimeModel struct {
	ID          entityID  `json:"id"`
	OwnerID     *entityID `json:"owner_id"`
	OwnerType   string    `json:"owner_type"`
	Name        string    `json:"model_name"`
	Capability  string    `json:"capability"`
	BaseURL     string    `json:"base_url"`
	Status      string    `json:"status"`
	InputPrice  float64   `json:"input_price_per_mtok"`
	OutputPrice float64   `json:"output_price_per_mtok"`
}

func (d *driver) runtimeReject(method, path string, caller account, input any, expected int) error {
	err := d.request(method, path, caller.token, input, nil)
	var rejected *apiError
	if !errors.As(err, &rejected) || rejected.status != expected || rejected.code == 0 {
		return fmt.Errorf("bot-runtime.reject %s %s: 期望HTTP%d，实际%v", method, path, expected, err)
	}
	return nil
}

func (d *driver) runtimeModelCRUD(owner, outsider account, id entityID, name string) error {
	path := "/models/" + id.String()
	if err := d.runtimeReject(http.MethodPut, path, outsider, map[string]string{"model_name": "stolen"}, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodDelete, path, outsider, nil, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]any{"model_name": name, "input_price_per_mtok": 2, "output_price_per_mtok": 3}, nil); err != nil {
		return err
	}
	var own struct {
		Items []runtimeModel `json:"items"`
	}
	if err := d.request(http.MethodGet, "/models?capability=chat", owner.token, nil, &own); err != nil {
		return err
	}
	if !slices.ContainsFunc(own.Items, func(m runtimeModel) bool {
		return m.ID == id && hasEntityID(m.OwnerID, owner.id) && m.OwnerType == "user" && m.Name == name && m.Capability == "chat" && m.Status == "active" && m.BaseURL == d.provider+"/v1" && m.InputPrice == 2 && m.OutputPrice == 3
	}) {
		return errors.New("models.update/list: UUID、所有权、模型配置或数字价格没有持久化")
	}
	var other struct {
		Items []runtimeModel `json:"items"`
	}
	if err := d.request(http.MethodGet, "/models", outsider.token, nil, &other); err != nil {
		return err
	}
	if slices.ContainsFunc(other.Items, func(m runtimeModel) bool { return m.ID == id }) {
		return errors.New("models.list: 私有模型泄露给其它用户")
	}
	for _, invalid := range []string{"0", "platform", "00000000-0000-0000-0000-000000000000"} {
		if err := d.runtimeReject(http.MethodPut, "/models/"+invalid, owner, map[string]string{"model_name": name}, http.StatusBadRequest); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) runtimeModelReferences(owner, outsider account, path string, botID, chatID, memoryID, embedID, foreignID entityID, streaming bool) error {
	if err := d.runtimeReject(http.MethodPut, path, outsider, map[string]string{"name": "stolen"}, http.StatusForbidden); err != nil {
		return err
	}
	for _, field := range []string{"model_id", "memory_model_id", "memory_embedding_model_id"} {
		for _, invalid := range []any{0, "", "platform"} {
			if err := d.runtimeReject(http.MethodPut, path, owner, map[string]any{field: invalid}, http.StatusBadRequest); err != nil {
				return err
			}
		}
		if err := d.runtimeReject(http.MethodPut, path, owner, map[string]any{field: foreignID}, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err := d.runtimeReject(http.MethodPost, "/bots", owner, map[string]any{"name": "forbidden-model", "type": "self_deployed", "model_id": foreignID}, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]any{"model_id": chatID, "memory_model_id": memoryID, "memory_embedding_model_id": embedID}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]string{"persona": "UUID reference preservation"}, nil); err != nil {
		return err
	}
	var preserved botView
	if err := d.request(http.MethodGet, path, owner.token, nil, &preserved); err != nil {
		return err
	}
	if preserved.ID != botID || !hasEntityID(preserved.ModelID, chatID) || !hasEntityID(preserved.MemoryModelID, memoryID) || !hasEntityID(preserved.MemoryEmbeddingModelID, embedID) {
		return errors.New("bot.references: 省略引用必须保持三个UUID")
	}
	for field, id := range map[string]entityID{"model_id": chatID, "memory_model_id": memoryID, "memory_embedding_model_id": embedID} {
		if err := d.runtimeReject(http.MethodPut, path, owner, map[string]any{field: id, "clear_" + field: true}, http.StatusBadRequest); err != nil {
			return err
		}
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]bool{"clear_memory_model_id": true}, nil); err != nil {
		return err
	}
	var partial botView
	if err := d.request(http.MethodGet, path, owner.token, nil, &partial); err != nil {
		return err
	}
	if !hasEntityID(partial.ModelID, chatID) || partial.MemoryModelID != nil || !hasEntityID(partial.MemoryEmbeddingModelID, embedID) {
		return errors.New("bot.references: 单引用clear不得改变其它引用")
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]bool{"clear_model_id": true, "clear_memory_embedding_model_id": true}, nil); err != nil {
		return err
	}
	var cleared botView
	if err := d.request(http.MethodGet, path, owner.token, nil, &cleared); err != nil {
		return err
	}
	if cleared.ModelID != nil || cleared.MemoryModelID != nil || cleared.MemoryEmbeddingModelID != nil {
		return errors.New("bot.references: clear未移除nullable模型引用")
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]any{
		"model_id": chatID, "response_triggers": []string{"always"}, "streaming_enabled": streaming,
		"enable_knowledge": false, "max_context_messages": 4, "temperature": 0.2,
	}, nil); err != nil {
		return err
	}
	return nil
}

func (d *driver) runtimeTokenState(owner account, token string, botID entityID, expected bool) error {
	var state struct {
		Valid   bool      `json:"valid"`
		BotID   *entityID `json:"bot_id"`
		OwnerID *entityID `json:"owner_id"`
	}
	if err := d.request(http.MethodPost, "/bots/token/validate", owner.token, map[string]string{"token": token}, &state); err != nil {
		return err
	}
	if state.Valid != expected || expected && (!hasEntityID(state.BotID, botID) || !hasEntityID(state.OwnerID, owner.id)) || !expected && (state.BotID != nil || state.OwnerID != nil) {
		return errors.New("bot.token: 配置禁用/恢复/删除后的token身份状态不匹配")
	}
	return nil
}

func (d *driver) runtimeMCPBindings(owner, outsider account, path string, serverID entityID) error {
	type binding struct {
		ID       entityID `json:"id"`
		ServerID entityID `json:"mcp_server_id"`
		Enabled  bool     `json:"enabled"`
	}
	read := func() ([]binding, error) {
		var result struct {
			Servers []binding `json:"servers"`
		}
		err := d.request(http.MethodGet, path+"/mcp-servers", owner.token, nil, &result)
		return result.Servers, err
	}
	servers, err := read()
	if err != nil {
		return err
	}
	if len(servers) != 1 || servers[0].ID == "" || servers[0].ServerID != serverID || !servers[0].Enabled {
		return errors.New("bot.mcp.bind: UUID绑定未持久化")
	}
	for _, enabled := range []bool{false, true} {
		if err := d.request(http.MethodPut, path+"/mcp-servers/"+serverID.String(), owner.token, map[string]bool{"enabled": enabled}, nil); err != nil {
			return err
		}
		updated, err := read()
		if err != nil {
			return err
		}
		if len(updated) != 1 || updated[0].ID != servers[0].ID || updated[0].ServerID != serverID || updated[0].Enabled != enabled {
			return errors.New("bot.mcp.bind: 启停改变绑定UUID或未持久化")
		}
	}
	if err := d.runtimeReject(http.MethodDelete, path+"/mcp-servers/"+serverID.String(), outsider, nil, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, path+"/mcp-servers/"+serverID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	unbound, err := read()
	if err != nil {
		return err
	}
	if len(unbound) != 0 {
		return errors.New("bot.mcp.unbind: 解绑后仍返回关联")
	}
	return d.request(http.MethodPost, path+"/mcp-servers", owner.token, map[string]any{"mcp_server_id": serverID}, nil)
}

func (d *driver) runtimeCapabilities(owner, outsider account, embeddingID entityID, suffix string) (result error) {
	conn, err := grpc.NewClient(d.llmRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	cli := llmpb.NewLLMGatewayClient(conn)
	contextFor := func(user account) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", user.id.String())), cancel
	}
	ownerID, outsiderID := owner.id.String(), outsider.id.String()
	ctx, cancel := contextFor(owner)
	embedded, err := cli.Embed(ctx, &llmpb.EmbedReq{ModelId: embeddingID.String(), OwnerId: &ownerID, Input: []string{"P5_KNOWN_ANCHOR"}})
	cancel()
	if err != nil {
		return fmt.Errorf("models.embedding: %w", err)
	}
	if embedded.Model != "fixture-embed-"+suffix || len(embedded.Data) != 1 || embedded.Data[0].Index != 0 || len(embedded.Data[0].Embedding) != 1536 || embedded.Data[0].Embedding[0] < 0.99 || embedded.Usage == nil || embedded.Usage.TotalTokens != 1 {
		return errors.New("models.embedding: UUID选择没有返回真实provider向量及usage")
	}
	ctx, cancel = contextFor(outsider)
	_, err = cli.Embed(ctx, &llmpb.EmbedReq{ModelId: embeddingID.String(), OwnerId: &outsiderID, Input: []string{"forbidden"}})
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("models.embedding.permission: 期望PermissionDenied，实际%v", err)
	}
	for _, capability := range []string{"rerank", "vlm"} {
		modelName := "fixture-" + capability + "-" + suffix
		id, err := d.fixtureModel(owner, capability, modelName)
		if err != nil {
			return err
		}
		defer func() {
			result = errors.Join(result, d.request(http.MethodDelete, "/models/"+id.String(), owner.token, nil, nil))
		}()
		ctx, cancel = contextFor(owner)
		if capability == "rerank" {
			response, callErr := cli.Rerank(ctx, &llmpb.RerankReq{ModelId: id.String(), OwnerId: &ownerID, Query: suffix, Documents: []string{"unrelated", suffix}, TopN: 1, ReturnDocuments: true})
			cancel()
			if callErr != nil {
				return fmt.Errorf("models.rerank: %w", callErr)
			}
			if response.Model != modelName || len(response.Results) != 1 || response.Results[0].Index != 1 || response.Results[0].Document != suffix || math.Abs(float64(response.Results[0].RelevanceScore)-0.95) > 0.00001 || response.Usage == nil || response.Usage.TotalTokens != 2 {
				return errors.New("models.rerank: UUID选择没有返回精确provider排名/文档/usage")
			}
			ctx, cancel = contextFor(outsider)
			_, err = cli.Rerank(ctx, &llmpb.RerankReq{ModelId: id.String(), OwnerId: &outsiderID, Query: suffix, Documents: []string{suffix}})
		} else {
			image := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jD1sAAAAASUVORK5CYII="
			response, callErr := cli.VlmChat(ctx, &llmpb.VlmChatReq{ModelId: id.String(), OwnerId: &ownerID, UserPrompt: suffix, ImageData: image})
			cancel()
			if callErr != nil {
				return fmt.Errorf("models.vlm: %w", callErr)
			}
			if response.Model != modelName || len(response.Choices) != 1 || response.Choices[0].Message == nil || response.Choices[0].Message.Content != "fixture-vision:"+suffix || response.Usage == nil || response.Usage.TotalTokens != 2 {
				return errors.New("models.vlm: UUID选择没有返回精确provider视觉内容/usage")
			}
			ctx, cancel = contextFor(outsider)
			_, err = cli.VlmChat(ctx, &llmpb.VlmChatReq{ModelId: id.String(), OwnerId: &outsiderID, UserPrompt: suffix, ImageData: image})
		}
		cancel()
		if status.Code(err) != codes.PermissionDenied {
			return fmt.Errorf("models.%s.permission: 期望PermissionDenied，实际%v", capability, err)
		}
		if err := d.runtimeCapabilityBilling(owner, id, modelName, capability); err != nil {
			return err
		}
	}
	return d.runtimeCapabilityBilling(owner, embeddingID, "fixture-embed-"+suffix, "embed")
}

type runtimeBillingRecord struct {
	ID           entityID  `json:"id"`
	ModelID      entityID  `json:"model_id"`
	BotID        *entityID `json:"bot_id"`
	OwnerID      *entityID `json:"owner_id"`
	OwnerType    string    `json:"owner_type"`
	ModelName    string    `json:"model_name"`
	Capability   string    `json:"capability"`
	InputTokens  int32     `json:"input_tokens"`
	OutputTokens int32     `json:"output_tokens"`
	InputCost    float64   `json:"input_cost"`
	OutputCost   float64   `json:"output_cost"`
	TotalCost    float64   `json:"total_cost"`
	CreatedAt    string    `json:"created_at"`
}

func (d *driver) runtimeBillingRecords(owner account) ([]runtimeBillingRecord, error) {
	var response struct {
		Items []runtimeBillingRecord `json:"items"`
		Total int32                  `json:"total"`
	}
	if err := d.request(http.MethodGet, "/models/billing/records?page=1&page_size=100", owner.token, nil, &response); err != nil {
		return nil, err
	}
	if response.Total != int32(len(response.Items)) {
		return nil, errors.New("billing.records: 数字分页total与实际记录不一致")
	}
	seen := make(map[entityID]bool, len(response.Items))
	for _, record := range response.Items {
		_, dateErr := time.Parse(time.RFC3339, record.CreatedAt)
		if record.ID == "" || record.ModelID == "" || seen[record.ID] || !hasEntityID(record.OwnerID, owner.id) || record.OwnerType != "user" || record.InputTokens <= 0 || record.OutputTokens < 0 || dateErr != nil {
			return nil, errors.New("billing.records: UUID/所有权/数字token/时间戳契约不匹配")
		}
		seen[record.ID] = true
		if math.IsNaN(record.TotalCost) || math.IsInf(record.TotalCost, 0) || math.Abs(record.InputCost-float64(record.InputTokens)*2/1_000_000) > 1e-12 || math.Abs(record.OutputCost-float64(record.OutputTokens)*3/1_000_000) > 1e-12 || math.Abs(record.TotalCost-record.InputCost-record.OutputCost) > 1e-12 {
			return nil, errors.New("billing.records: 数字价格、token和费用不一致")
		}
	}
	return response.Items, nil
}

func (d *driver) runtimeCapabilityBilling(owner account, modelID entityID, name, capability string) error {
	records, err := d.runtimeBillingRecords(owner)
	if err != nil {
		return err
	}
	matching := 0
	for _, record := range records {
		if record.ModelID == modelID {
			if record.BotID != nil || record.ModelName != name || record.Capability != capability {
				return errors.New("billing.capability: direct调用不得伪造bot引用且必须关联选定模型")
			}
			matching++
		}
	}
	if matching != 1 {
		return fmt.Errorf("billing.capability: 期望一个%s实际调用记录，得到%d", capability, matching)
	}
	return nil
}

func (d *driver) runtimeBilling(owner, outsider account, botID, modelID entityID, name string, expected int) error {
	records, err := d.runtimeBillingRecords(owner)
	if err != nil {
		return err
	}
	count := 0
	var input, output int64
	var cost float64
	for _, record := range records {
		if hasEntityID(record.BotID, botID) {
			if record.ModelID != modelID || record.ModelName != name || record.Capability != "chat" {
				return errors.New("billing.bot: model/bot/user三个UUID未指向同一次实际调用")
			}
			count++
			input += int64(record.InputTokens)
			output += int64(record.OutputTokens)
			cost += record.TotalCost
		}
	}
	if count != expected || input != int64(expected) || output != int64(expected) {
		return fmt.Errorf("billing.bot: %d次provider请求应各记录1输入/1输出，实际count=%d input=%d output=%d", expected, count, input, output)
	}
	var stats struct {
		Input  int64   `json:"total_input_tokens,string"`
		Output int64   `json:"total_output_tokens,string"`
		Cost   float64 `json:"total_cost"`
	}
	if err := d.request(http.MethodGet, "/models/billing/stats?bot_id="+botID.String(), owner.token, nil, &stats); err != nil {
		return err
	}
	if stats.Input != input || stats.Output != output || math.Abs(stats.Cost-cost) > 1e-12 {
		return errors.New("billing.stats: 指定UUID Bot的汇总与持久化记录不一致")
	}
	other, err := d.runtimeBillingRecords(outsider)
	if err != nil {
		return err
	}
	if len(other) != 0 {
		return errors.New("billing.permission: 其它用户看到私有调用记录")
	}
	return nil
}

// Only timestamps/quantities accept the protobuf int64 text representation or
// numeric async-event representation. Entity identities still use entityID only.
type protocolInteger int64

func (value *protocolInteger) UnmarshalJSON(raw []byte) error {
	text := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return errors.New("timestamp/quantity必须是整数，不是实体身份")
	}
	*value = protocolInteger(number)
	return nil
}

type runtimeTodo struct {
	ID        entityID        `json:"id"`
	SummaryID *entityID       `json:"summary_id"`
	ConvID    entityID        `json:"conv_id"`
	Content   string          `json:"content"`
	Done      bool            `json:"done"`
	CreatedAt protocolInteger `json:"created_at"`
	UpdatedAt protocolInteger `json:"updated_at"`
}

type runtimeSummary struct {
	ID            *entityID       `json:"summary_id"`
	Summary       string          `json:"summary"`
	Todos         []runtimeTodo   `json:"todos"`
	TotalMessages int32           `json:"total_messages"`
	CreatedAt     protocolInteger `json:"created_at"`
	Status        string          `json:"status"`
}

type runtimeSummaryList struct {
	Items      []runtimeSummary `json:"items"`
	Standalone []runtimeTodo    `json:"standalone_todos"`
}

func (d *driver) runtimeConversationTools(owner, outsider account, address, secondAddress string, convID entityID, sent sentMessage, modelID entityID) error {
	settingsPath := "/users/me/settings"
	if err := d.request(http.MethodPut, settingsPath, owner.token, map[string]any{"ai_model_id": modelID}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, settingsPath, owner.token, map[string]string{"theme": "dark"}, nil); err != nil {
		return err
	}
	var settings struct {
		ModelID *entityID `json:"ai_model_id"`
	}
	if err := d.request(http.MethodGet, settingsPath, owner.token, nil, &settings); err != nil {
		return err
	}
	if !hasEntityID(settings.ModelID, modelID) {
		return errors.New("tools.settings: 省略AI模型引用必须保持UUID")
	}
	if err := d.runtimeReject(http.MethodPut, settingsPath, owner, map[string]any{"ai_model_id": modelID, "clear_ai_model_id": true}, http.StatusBadRequest); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, settingsPath, owner.token, map[string]bool{"clear_ai_model_id": true}, nil); err != nil {
		return err
	}
	settings.ModelID = nil
	if err := d.request(http.MethodGet, settingsPath, owner.token, nil, &settings); err != nil {
		return err
	}
	if settings.ModelID != nil {
		return errors.New("tools.settings: clear没有移除AI模型引用")
	}
	if err := d.request(http.MethodPut, settingsPath, owner.token, map[string]any{"ai_model_id": modelID}, nil); err != nil {
		return err
	}
	deviceA, err := d.inboxLoginDevice(owner, "tools_a")
	if err != nil {
		return err
	}
	deviceB, err := d.inboxLoginDevice(owner, "tools_b")
	if err != nil {
		return err
	}
	primary, err := d.p6Connect(address, deviceA)
	if err != nil {
		return err
	}
	defer primary.close()
	secondary, err := d.p6Connect(secondAddress, deviceB)
	if err != nil {
		return err
	}
	defer secondary.close()
	observer, err := d.p6Connect(secondAddress, outsider)
	if err != nil {
		return err
	}
	defer observer.close()
	path := "/convs/" + convID.String()
	read := func() (runtimeSummaryList, error) {
		var summaries runtimeSummaryList
		err := d.request(http.MethodGet, path+"/summaries", owner.token, nil, &summaries)
		return summaries, err
	}
	for _, operation := range []struct {
		method, endpoint string
		body             any
	}{
		{http.MethodPost, path + "/summarize", map[string]bool{"all": true}},
		{http.MethodGet, path + "/summaries", nil},
		{http.MethodPost, path + "/todos", map[string]string{"content": "forbidden"}},
		{http.MethodPost, path + "/reply-candidates", map[string]any{}},
		{http.MethodPost, "/messages/" + sent.MessageID.String() + "/translate", map[string]string{"text": sent.Content.Text, "target_lang": "en-US"}},
	} {
		if err := d.runtimeReject(operation.method, operation.endpoint, outsider, operation.body, http.StatusForbidden); err != nil {
			return err
		}
	}
	var processing runtimeSummary
	if err := d.request(http.MethodPost, path+"/summarize", owner.token, map[string]bool{"all": true}, &processing); err != nil {
		return err
	}
	if processing.Status != "processing" || processing.ID != nil {
		return errors.New("tools.summary: processing必须省略尚未持久化的summary_id")
	}
	var completed event
	for _, socket := range []*p6Socket{primary, secondary} {
		evt, err := d.p6Wait(socket, 0, "summary.done", func(e p6Event) bool {
			return hasEntityID(e.ConvID, convID) && (e.Type == "conv.summarize.done" || e.Type == "conv.summarize.failed")
		})
		if err != nil {
			return err
		}
		if evt.Type != "conv.summarize.done" || evt.SummaryID == nil || evt.Summary == "" || evt.TotalMessages != 2 || len(evt.Todos) != 1 || evt.Todos[0].Content != "fixture-todo: verify UUID references" || !hasEntityID(evt.Todos[0].SummaryID, *evt.SummaryID) || evt.Todos[0].ConvID != convID || evt.Todos[0].ID == "" || evt.Todos[0].Done || evt.CreatedAt <= 0 {
			return errors.New("tools.summary: WS没有返回精确provider摘要/todo及有效UUID关联")
		}
		if completed.SummaryID != nil && (*completed.SummaryID != *evt.SummaryID || completed.Summary != evt.Summary || completed.Todos[0].ID != evt.Todos[0].ID || completed.TotalMessages != evt.TotalMessages) {
			return errors.New("tools.summary: 同账号设备WS结果身份或内容不一致")
		}
		completed = evt.event
	}
	summaries, err := read()
	if err != nil {
		return err
	}
	if len(summaries.Items) != 1 || !hasEntityID(summaries.Items[0].ID, *completed.SummaryID) || summaries.Items[0].Summary != completed.Summary || summaries.Items[0].Status != "completed" || summaries.Items[0].TotalMessages != completed.TotalMessages || len(summaries.Items[0].Todos) != 1 || summaries.Items[0].Todos[0].ID != completed.Todos[0].ID {
		return errors.New("tools.summary: REST持久化读取与两个设备WS结果不一致")
	}
	for _, summaryID := range []*entityID{nil, completed.SummaryID} {
		input := map[string]any{"content": "fixture-manual-todo"}
		if summaryID != nil {
			input["summary_id"] = *summaryID
		}
		var created runtimeTodo
		if err := d.request(http.MethodPost, path+"/todos", owner.token, input, &created); err != nil {
			return err
		}
		if created.ID == "" || created.ConvID != convID || created.Content != "fixture-manual-todo" || created.Done || created.CreatedAt <= 0 || (summaryID == nil) != (created.SummaryID == nil) || summaryID != nil && !hasEntityID(created.SummaryID, *summaryID) {
			return errors.New("tools.todo.create: nullable summary关联或UUID/content契约不匹配")
		}
		todoPath := path + "/todos/" + created.ID.String()
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			if err := d.runtimeReject(method, todoPath, outsider, map[string]string{"content": "forbidden"}, http.StatusForbidden); err != nil {
				return err
			}
		}
		check := func(content string, done, present bool) error {
			listed, err := read()
			if err != nil {
				return err
			}
			todos := listed.Standalone
			if summaryID != nil {
				index := slices.IndexFunc(listed.Items, func(s runtimeSummary) bool { return hasEntityID(s.ID, *summaryID) })
				if index < 0 {
					return errors.New("tools.todo: summary持久化读取丢失")
				}
				todos = listed.Items[index].Todos
			}
			index := slices.IndexFunc(todos, func(t runtimeTodo) bool { return t.ID == created.ID })
			if !present {
				if index >= 0 {
					return errors.New("tools.todo.delete: 删除后刷新仍存在")
				}
				return nil
			}
			if index < 0 || todos[index].Content != content || todos[index].Done != done || todos[index].ConvID != convID || (todos[index].SummaryID == nil) != (summaryID == nil) || summaryID != nil && !hasEntityID(todos[index].SummaryID, *summaryID) {
				return errors.New("tools.todo: 真实DB刷新未保持content/done/nullable UUID关联")
			}
			return nil
		}
		if err := check(created.Content, false, true); err != nil {
			return err
		}
		for _, update := range []struct {
			body    any
			content string
			done    bool
		}{
			{map[string]bool{"done": true}, created.Content, true},
			{map[string]string{"content": "fixture-edited-todo"}, "fixture-edited-todo", true},
			{map[string]bool{"done": false}, "fixture-edited-todo", false},
		} {
			if err := d.request(http.MethodPut, todoPath, owner.token, update.body, nil); err != nil {
				return err
			}
			if err := check(update.content, update.done, true); err != nil {
				return err
			}
		}
		if err := d.request(http.MethodDelete, todoPath, owner.token, nil, nil); err != nil {
			return err
		}
		if err := check("", false, false); err != nil {
			return err
		}
	}
	var translating struct {
		Status string `json:"status"`
	}
	if err := d.request(http.MethodPost, "/messages/"+sent.MessageID.String()+"/translate", owner.token, map[string]string{"text": sent.Content.Text, "target_lang": "en-US"}, &translating); err != nil {
		return err
	}
	if translating.Status != "processing" {
		return errors.New("tools.translate: REST未返回processing")
	}
	for _, socket := range []*p6Socket{primary, secondary} {
		evt, err := d.p6Wait(socket, 0, "translate.done", func(e p6Event) bool {
			return hasEntityID(e.MsgID, sent.MessageID) && (e.Type == "conv.translate.done" || e.Type == "conv.translate.failed")
		})
		if err != nil {
			return err
		}
		if evt.Type != "conv.translate.done" || evt.TranslatedText != "fixture-translation:"+sent.Content.Text || evt.DetectedLang != "en-US" {
			return errors.New("tools.translate: WS的消息UUID、翻译或语言与请求不一致")
		}
	}
	var candidates struct {
		Status string `json:"status"`
	}
	if err := d.request(http.MethodPost, path+"/reply-candidates", owner.token, map[string]any{"reply_to_msg_id": sent.MessageID}, &candidates); err != nil {
		return err
	}
	if candidates.Status != "processing" {
		return errors.New("tools.reply-candidates: REST未返回processing")
	}
	for _, socket := range []*p6Socket{primary, secondary} {
		evt, err := d.p6Wait(socket, 0, "reply-candidates.done", func(e p6Event) bool {
			return hasEntityID(e.ConvID, convID) && (e.Type == "conv.reply_candidates.done" || e.Type == "conv.reply_candidates.failed")
		})
		if err != nil {
			return err
		}
		if evt.Type != "conv.reply_candidates.done" || !slices.Equal(evt.Candidates, []string{"收到", "继续执行", "核对结果"}) {
			return errors.New("tools.reply-candidates: WS未返回实际provider候选回复")
		}
	}
	if err := d.p6NoConversation(observer, convID, 0); err != nil {
		return err
	}
	observer.mu.Lock()
	leaked := slices.ContainsFunc(observer.events, func(e p6Event) bool { return hasEntityID(e.MsgID, sent.MessageID) })
	observer.mu.Unlock()
	if leaked {
		return errors.New("tools.permission: 非成员收到私有翻译事件")
	}
	return nil
}

func (d *driver) runtimePrivateBotConversation(owner, outsider account, address string, botID entityID, suffix string) (result error) {
	if err := d.runtimeReject(http.MethodPost, "/convs", outsider, map[string]any{"type": "single", "bot_id": botID}, http.StatusForbidden); err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "single", "bot_id": botID}, &conv); err != nil {
		return err
	}
	if conv.ID == "" {
		return errors.New("bot.single: typed Bot单聊未返回UUID")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/convs/"+conv.ID.String(), owner.token, nil, nil))
	}()
	var repeated struct {
		ID entityID `json:"conversation_id"`
	}
	if err := d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "single", "bot_id": botID}, &repeated); err != nil {
		return err
	}
	if repeated.ID != conv.ID {
		return errors.New("bot.single: 重复打开typed Bot单聊改变会话UUID")
	}
	user, err := d.inboxLoginDevice(owner, "bot_single")
	if err != nil {
		return err
	}
	socket, err := d.p6Connect(address, user)
	if err != nil {
		return err
	}
	defer socket.close()
	sent, err := d.sendMessage(owner, conv.ID, "P5_BOT_REQUEST_"+suffix+"_single", 0)
	if err != nil {
		return err
	}
	evt, err := d.p6Wait(socket, 0, "bot.single.reply", func(e p6Event) bool {
		return e.Type == "message.new" && hasEntityID(e.ConvID, conv.ID) && hasEntityID(e.Message.SenderID, botID) && hasEntityID(e.Message.ReplyToID, sent.MessageID)
	})
	if err != nil {
		return err
	}
	if evt.Message.MessageID == "" || evt.Message.ConvID != conv.ID || evt.Message.Seq <= sent.Seq || evt.Message.Content.Text != "fixture-reply:"+sent.Content.Text {
		return errors.New("bot.single: typed Bot单聊没有精确provider回复与消息UUID")
	}
	reply := sentMessage{MessageID: evt.Message.MessageID, ConvID: conv.ID, SenderID: botID, Seq: evt.Message.Seq}
	reply.Content.Text = evt.Message.Content.Text
	return d.conversationMessage("bot.single.read", owner, reply)
}
