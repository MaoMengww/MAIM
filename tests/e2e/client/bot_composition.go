package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Sources retain the retrieve identity, not just a UUID-shaped value or title.
type runtimeKnowledgeSource struct {
	Type    string   `json:"type"`
	KBID    entityID `json:"kb_id"`
	DocID   entityID `json:"doc_id"`
	ChunkID entityID `json:"chunk_id"`
	KBName  string   `json:"kb_name"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
}

type runtimeBotContent struct {
	BotID entityID `json:"bot_id"`
	Text  string   `json:"text"`
	Raw   string   `json:"raw_payload"`
}

func runtimeSourcePayload(raw string, expected []runtimeKnowledgeSource) error {
	var payload struct {
		Sources []runtimeKnowledgeSource `json:"kb_sources"`
		Tools   []string                 `json:"tool_names"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Errorf("p7.sources.raw: %w", err)
	}
	if !slices.Equal(payload.Sources, expected) || !slices.Contains(payload.Tools, "fixture_echo") {
		return errors.New("p7.sources.raw: 持久来源不是 Retrieve/WS 同一 UUID 对象，或缺少网络工具")
	}
	return nil
}

// A fresh WS connection per exchange ensures no earlier event satisfies a probe.
// The fixture can answer only from AIM's dedicated knowledge/memory context,
// never from previous chat text or fixture-side persistence.
func (d *driver) runtimeCompositionExchange(caller account, address string, botID, convID entityID, text string, sources []runtimeKnowledgeSource) (string, error) {
	conn, err := d.connect(address, caller)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := d.ready(conn); err != nil {
		return "", err
	}
	sent, err := d.sendMessage(caller, convID, text, 0)
	if err != nil {
		return "", err
	}
	if err := conn.SetReadDeadline(time.Now().Add(d.timeout)); err != nil {
		return "", err
	}
	var replyID, doneID entityID
	var streamID, replyText, chunks, raw string
	var streamedSources []runtimeKnowledgeSource
	var previous int64 = -1
	for replyID == "" || doneID == "" {
		var evt struct {
			Type      string    `json:"type"`
			ConvID    *entityID `json:"conv_id"`
			BotID     *entityID `json:"bot_id"`
			ReplyToID *entityID `json:"reply_to_msg_id"`
			MessageID entityID  `json:"message_id"`
			StreamID  string    `json:"stream_id"`
			Seq       int64     `json:"seq"`
			Content   string    `json:"content"`
			Message   struct {
				ID        entityID          `json:"message_id"`
				ConvID    entityID          `json:"conv_id"`
				SenderID  *entityID         `json:"sender_id"`
				ReplyToID *entityID         `json:"reply_to_msg_id"`
				Seq       sequenceNumber    `json:"seq"`
				Content   runtimeBotContent `json:"content"`
			} `json:"message"`
		}
		if err := conn.ReadJSON(&evt); err != nil {
			return "", fmt.Errorf("p7.bot.ws: %w", err)
		}
		if evt.Type == "error" {
			return "", errors.New("p7.bot.ws: error事件")
		}
		if !hasEntityID(evt.ConvID, convID) {
			continue
		}
		if evt.Type == "message.new" && hasEntityID(evt.Message.SenderID, botID) && hasEntityID(evt.Message.ReplyToID, sent.MessageID) {
			if evt.Message.ID == "" || evt.Message.ConvID != convID || evt.Message.Seq <= sent.Seq || evt.Message.Content.BotID != botID {
				return "", errors.New("p7.bot.ws: 回复和来源所属 Bot/会话/消息身份失配")
			}
			replyID, replyText, raw = evt.Message.ID, evt.Message.Content.Text, evt.Message.Content.Raw
		}
		if !hasEntityID(evt.BotID, botID) {
			continue
		}
		switch evt.Type {
		case "bot.streaming.chunk", "bot.streaming.sources", "bot.streaming.tool_used", "bot.streaming.done":
			if evt.StreamID == "" || evt.Seq <= previous {
				return "", errors.New("p7.bot.stream: 流身份或顺序无效")
			}
			if streamID == "" {
				if !hasEntityID(evt.ReplyToID, sent.MessageID) {
					return "", errors.New("p7.bot.stream: 首事件原消息 UUID 失配")
				}
				streamID = evt.StreamID
			} else if evt.StreamID != streamID {
				return "", errors.New("p7.bot.stream: 不同请求流被混用")
			}
			previous = evt.Seq
			switch evt.Type {
			case "bot.streaming.chunk":
				chunks += evt.Content
			case "bot.streaming.sources":
				if err := json.Unmarshal([]byte(evt.Content), &streamedSources); err != nil {
					return "", fmt.Errorf("p7.sources.ws: %w", err)
				}
			case "bot.streaming.done":
				doneID = evt.MessageID
			}
		}
	}
	if doneID != replyID || chunks != replyText || !slices.Equal(streamedSources, sources) {
		return "", errors.New("p7.bot.stream: done/正文/来源与持久回复失配")
	}
	if err := runtimeSourcePayload(raw, sources); err != nil {
		return "", err
	}
	var history struct {
		Messages []struct {
			storedMessage
			Bot *runtimeBotContent `json:"bot"`
		} `json:"messages"`
	}
	if err := d.request(http.MethodGet, "/convs/"+convID.String()+"/messages?cursor=0&limit=50", caller.token, nil, &history); err != nil {
		return "", err
	}
	for _, msg := range history.Messages {
		if msg.MessageID != replyID {
			continue
		}
		if msg.ConvID != convID || !hasEntityID(msg.SenderID, botID) || !hasEntityID(msg.ReplyToID, sent.MessageID) || msg.Bot == nil || msg.Bot.BotID != botID || msg.Bot.Text != replyText || msg.Bot.Raw != raw {
			return "", errors.New("p7.bot.history: WS 来源和正文未还原到同一 Bot/会话/回复 UUID")
		}
		return replyText, runtimeSourcePayload(msg.Bot.Raw, sources)
	}
	return "", errors.New("p7.bot.history: WS 回复 UUID 不在真实持久历史中")
}

func (d *driver) runtimeKnowledgeMemory(owner, outsider account, address string, mcpID, embeddingID entityID, suffix string) (result error) {
	chatName, extractName := "fixture-composition-"+suffix, "fixture-memory-extract-"+suffix
	chatID, err := d.fixtureModel(owner, "chat", chatName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+chatID.String(), owner.token, nil, nil))
	}()
	extractID, err := d.fixtureModel(owner, "chat", extractName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+extractID.String(), owner.token, nil, nil))
	}()
	var bots []entityID
	defer func() {
		for _, id := range bots {
			result = errors.Join(result, d.request(http.MethodDelete, "/bots/"+id.String(), owner.token, nil, nil))
		}
	}()
	for range 2 {
		var bot botView
		if err := d.request(http.MethodPost, "/bots", owner.token, map[string]any{
			"name": "p7-identical-bot-name-" + suffix, "type": "self_deployed", "model_id": chatID, "model_name": chatName,
			"system_prompt":     "Use fixture_echo for the exact user message before answering from supplied context.",
			"response_triggers": []string{"always"}, "streaming_enabled": true, "enable_knowledge": true,
			"memory_model_id": extractID, "memory_model_name": extractName, "memory_embedding_model_id": embeddingID,
		}, &bot); err != nil {
			return err
		}
		if bot.ID == "" || !hasEntityID(bot.OwnerID, owner.id) || !hasEntityID(bot.ModelID, chatID) || !hasEntityID(bot.MemoryModelID, extractID) || !hasEntityID(bot.MemoryEmbeddingModelID, embeddingID) {
			return errors.New("p7.bot.config: 知识与记忆模型实际引用身份不匹配")
		}
		bots = append(bots, bot.ID)
		if err := d.request(http.MethodPost, "/bots/"+bot.ID.String()+"/mcp-servers", owner.token, map[string]any{"mcp_server_id": mcpID}, nil); err != nil {
			return err
		}
	}
	var convs []entityID
	defer func() {
		for _, id := range convs {
			result = errors.Join(result, d.request(http.MethodDelete, "/convs/"+id.String(), owner.token, nil, nil))
		}
	}()
	for i := range 3 {
		var conv struct {
			ID entityID `json:"conversation_id"`
		}
		if err := d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "group", "group_name": "p7-composition-" + suffix}, &conv); err != nil {
			return err
		}
		if conv.ID == "" {
			return errors.New("p7.conv: 缺少会话 UUID")
		}
		convs = append(convs, conv.ID)
		botID := bots[0]
		if i == 2 {
			botID = bots[1]
		}
		if err := d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", owner.token, map[string]any{"bot_id": botID}, nil); err != nil {
			return err
		}
	}
	var kb struct {
		ID      entityID  `json:"id"`
		OwnerID *entityID `json:"owner_id"`
	}
	if err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
		"name": "p7-composition-" + suffix, "mode": "rag", "embedding_model_id": embeddingID, "embedding_model": "fixture-embed-" + suffix,
		"pipeline_config": map[string]any{"parsing": map[string]any{"engines": []string{"builtin"}}, "chunking": map[string]any{"chunk_size": 1024, "overlap": 0}, "retrieval": map[string]any{"mode": "vector", "top_k": 5, "candidate_top_k": 10, "score_threshold": 0.7}},
	}, &kb); err != nil {
		return err
	}
	if kb.ID == "" || !hasEntityID(kb.OwnerID, owner.id) {
		return errors.New("p7.kb: 知识所属用户 UUID 失配")
	}
	basePath := "/knowledge/bases/" + kb.ID.String()
	defer func() { result = errors.Join(result, d.request(http.MethodDelete, basePath, owner.token, nil, nil)) }()
	doc, err := d.uploadKnowledge(owner, kb.ID, "p7-source.txt", "p7-source-"+suffix, knownKnowledge)
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, doc, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	if err := d.broadcastPoll("p7.knowledge.visible", func(bounded *driver) (bool, error) {
		err := bounded.searchKnownKnowledge(owner, kb.ID, doc.ID, doc.Title)
		if errors.Is(err, errKnownUnavailable) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		return err
	}
	var chunks struct {
		Items []knowledgeChunkView `json:"chunks"`
	}
	if err := d.request(http.MethodGet, "/knowledge/documents/"+doc.ID.String()+"/chunks", owner.token, nil, &chunks); err != nil {
		return err
	}
	if len(chunks.Items) != 1 || chunks.Items[0].DocID != doc.ID || chunks.Items[0].Content != knownKnowledge {
		return errors.New("p7.chunk: 实际关系片段和文档身份失配")
	}
	sources := []runtimeKnowledgeSource{{Type: "rag", KBID: kb.ID, DocID: doc.ID, ChunkID: chunks.Items[0].ID, KBName: "p7-composition-" + suffix, Title: doc.Title, Content: knownKnowledge}}
	botPath, convPath := "/bots/"+bots[0].String()+"/knowledge", "/convs/"+convs[0].String()+"/knowledge"
	for _, target := range []struct {
		path, kind string
		id         entityID
	}{{botPath, "bot", bots[0]}, {convPath, "conv", convs[0]}} {
		if err := d.request(http.MethodPost, target.path, owner.token, map[string]any{"kb_id": kb.ID}, nil); err != nil {
			return err
		}
		var bindings struct {
			Items []struct {
				KBID     entityID `json:"kb_id"`
				TargetID entityID `json:"target_id"`
				Kind     string   `json:"target_type"`
			} `json:"items"`
		}
		if err := d.request(http.MethodGet, target.path, owner.token, nil, &bindings); err != nil {
			return err
		}
		if len(bindings.Items) != 1 || bindings.Items[0].KBID != kb.ID || bindings.Items[0].TargetID != target.id || bindings.Items[0].Kind != target.kind {
			return errors.New("p7.binding: 高层绑定没有返回同一 KB/Bot/会话 UUID")
		}
		query := "P7_KNOWLEDGE_QUERY P5_KNOWN_ANCHOR " + suffix
		actual, err := d.runtimeCompositionExchange(owner, address, bots[0], convs[0], query, sources)
		if err != nil {
			return err
		}
		if actual != "fixture-reply:"+query+"|knowledge:cobalt-47" {
			return errors.New("p7.knowledge: Bot绑定及组合绑定未使用真实来源")
		}
	}
	if err := d.runtimeCompositionStream(owner, bots[0], convs[0], "P7_KNOWLEDGE_QUERY P5_KNOWN_ANCHOR SSE "+suffix, sources); err != nil {
		return err
	}
	var reverse struct {
		Items []struct {
			ID   entityID `json:"target_id"`
			Kind string   `json:"target_type"`
		} `json:"items"`
	}
	if err := d.request(http.MethodGet, basePath+"/bindings", owner.token, nil, &reverse); err != nil {
		return err
	}
	if len(reverse.Items) != 2 || !slices.ContainsFunc(reverse.Items, func(row struct {
		ID   entityID `json:"target_id"`
		Kind string   `json:"target_type"`
	}) bool {
		return row.Kind == "bot" && row.ID == bots[0]
	}) || !slices.ContainsFunc(reverse.Items, func(row struct {
		ID   entityID `json:"target_id"`
		Kind string   `json:"target_type"`
	}) bool {
		return row.Kind == "conv" && row.ID == convs[0]
	}) {
		return errors.New("p7.binding.reverse: KB反查目标 UUID 失配")
	}
	if err := d.runtimeReject(http.MethodPost, "/bots/"+bots[1].String()+"/knowledge", outsider, map[string]any{"kb_id": kb.ID}, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, basePath+"/bindings", outsider, nil, http.StatusForbidden); err != nil {
		return err
	}
	isolationQuery := "P7_KNOWLEDGE_QUERY P5_KNOWN_ANCHOR " + suffix
	isolationReply, err := d.runtimeCompositionExchange(owner, address, bots[1], convs[2], isolationQuery, nil)
	if err != nil {
		return err
	}
	if isolationReply != "fixture-reply:"+isolationQuery+"|knowledge:none" {
		return errors.New("p7.knowledge.isolation: 同名另一Bot使用了未绑定知识UUID")
	}
	query := "P7_KNOWLEDGE_QUERY P5_KNOWN_ANCHOR " + suffix
	for i, path := range []string{botPath, convPath} {
		if err := d.request(http.MethodDelete, path+"/"+kb.ID.String(), owner.token, nil, nil); err != nil {
			return err
		}
		expectedSources, value := sources, "cobalt-47"
		if i == 1 {
			expectedSources, value = nil, "none"
		}
		actual, err := d.runtimeCompositionExchange(owner, address, bots[0], convs[0], query, expectedSources)
		if err != nil {
			return err
		}
		if actual != "fixture-reply:"+query+"|knowledge:"+value {
			return errors.New("p7.knowledge: 绑定/解绑后的增强正文不是实际检索内容")
		}
	}
	for _, path := range []string{botPath, convPath, basePath + "/bindings"} {
		var empty struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := d.request(http.MethodGet, path, owner.token, nil, &empty); err != nil {
			return err
		}
		if len(empty.Items) != 0 {
			return errors.New("p7.binding.unbind: 正向/反向仍引用已解绑UUID")
		}
	}
	// Store in one conversation and recall in another: ordinary chat history cannot
	// masquerade as memory persistence. A second same-named Bot must remain empty.
	value := "cobalt-" + suffix
	storeText := "P7_MEMORY_STORE " + value
	actual, err := d.runtimeCompositionExchange(owner, address, bots[0], convs[0], storeText, nil)
	if err != nil {
		return err
	}
	if actual != "fixture-reply:"+storeText {
		return errors.New("p7.memory.store: Bot没有完成真实provider/tool回复")
	}
	memoryQuery := "P7_MEMORY_QUERY durable preference " + suffix
	if err := d.broadcastPoll("p7.memory.restore", func(bounded *driver) (bool, error) {
		actual, err := bounded.runtimeCompositionExchange(owner, address, bots[0], convs[1], memoryQuery, nil)
		if err != nil {
			return false, err
		}
		if actual == "fixture-reply:"+memoryQuery+"|memory:none" {
			return false, nil
		}
		if actual != "fixture-reply:"+memoryQuery+"|memory:"+value {
			return false, errors.New("p7.memory: 恢复了错误用户/Bot事实")
		}
		return true, nil
	}); err != nil {
		return err
	}
	if err := d.request(http.MethodPost, botPath, owner.token, map[string]any{"kb_id": kb.ID}, nil); err != nil {
		return err
	}
	combinedQuery := memoryQuery + " P5_KNOWN_ANCHOR"
	actual, err = d.runtimeCompositionExchange(owner, address, bots[0], convs[1], combinedQuery, sources)
	if err != nil {
		return err
	}
	if actual != "fixture-reply:"+combinedQuery+"|memory:"+value+"|knowledge:cobalt-47" {
		return errors.New("p7.composition: 同一次Bot回复未同时恢复真实知识与记忆")
	}
	if err := d.request(http.MethodDelete, botPath+"/"+kb.ID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/convs/"+convs[1].String()+"/members/invite", owner.token, map[string]any{"user_ids": []entityID{outsider.id}}, nil); err != nil {
		return err
	}
	for _, probe := range []struct {
		user      account
		bot, conv entityID
	}{{outsider, bots[0], convs[1]}, {owner, bots[1], convs[2]}} {
		actual, err := d.runtimeCompositionExchange(probe.user, address, probe.bot, probe.conv, memoryQuery, nil)
		if err != nil {
			return err
		}
		if actual != "fixture-reply:"+memoryQuery+"|memory:none" {
			return errors.New("p7.memory.isolation: 同会话另一用户/同名另一Bot泄露持久记忆")
		}
	}
	observed, err := d.observeProvider()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(observed.MemoryExtractions, func(row struct {
		Model   string   `json:"model"`
		UserID  entityID `json:"user_id"`
		Message string   `json:"message"`
	}) bool {
		return row.Model == extractName && row.UserID == owner.id && row.Message == storeText
	}) || !slices.Contains(observed.ToolCallIDs, "fixture-call") {
		return errors.New("p7.provider: 未观测真实目标用户抽取或专用tool-call原值")
	}
	fmt.Printf("P7 evidence: bot_id=%s user_id=%s source_conv=%s recall_conv=%s memory_message=%q; knowledge kb=%s doc=%s chunk=%s; WS/history来源与解绑、记忆恢复和跨用户/Bot隔离 PASS；Neo4j关系/Milvus实体ID回读由domain真实存储验收另证\n", bots[0], owner.id, convs[0], convs[1], storeText, kb.ID, doc.ID, chunks.Items[0].ID)
	return nil
}

// The GET/SSE入口 must bind query UUIDs and reach runtime, not accept a JSON body.
func (d *driver) runtimeCompositionStream(caller account, botID, convID entityID, text string, expected []runtimeKnowledgeSource) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	query := url.Values{"conv_id": {convID.String()}, "message": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.gateway+"/api/v1/bots/"+botID.String()+"/chat/stream?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+caller.token)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("p7.stream: HTTP %d", resp.StatusCode)
	}
	var sources []runtimeKnowledgeSource
	var doneID entityID
	var chunks, final string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "event:error" {
			return errors.New("p7.stream: upstream failed")
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var event struct {
			Type      string    `json:"type"`
			Content   string    `json:"content"`
			ConvID    entityID  `json:"conv_id"`
			MessageID *entityID `json:"message_id"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		if event.ConvID != convID {
			return errors.New("p7.stream: 会话UUID失配")
		}
		switch event.Type {
		case "chunk":
			chunks += event.Content
		case "sources":
			if err := json.Unmarshal([]byte(event.Content), &sources); err != nil {
				return err
			}
		case "done":
			if event.MessageID == nil {
				return errors.New("p7.stream: 无持久回复UUID")
			}
			doneID, final = *event.MessageID, event.Content
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if doneID == "" || chunks != final || final != "fixture-reply:"+text+"|knowledge:cobalt-47" || !slices.Equal(sources, expected) {
		return errors.New("p7.stream: 增强正文或来源未还原真实知识身份")
	}
	var history struct {
		Messages []struct {
			MessageID entityID           `json:"message_id"`
			Bot       *runtimeBotContent `json:"bot"`
		} `json:"messages"`
	}
	if err := d.request(http.MethodGet, "/convs/"+convID.String()+"/messages?cursor=0&limit=50", caller.token, nil, &history); err != nil {
		return err
	}
	for _, msg := range history.Messages {
		if msg.MessageID == doneID && msg.Bot != nil && msg.Bot.BotID == botID && msg.Bot.Text == final {
			return runtimeSourcePayload(msg.Bot.Raw, expected)
		}
	}
	return errors.New("p7.stream: SSE回复与持久正文/来源失配")
}
