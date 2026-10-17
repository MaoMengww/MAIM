package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

// Sources retain the retrieve identity, not just a UUID-shaped value or title.
type runtimeKnowledgeSource struct {
	Type     string   `json:"type"`
	KBID     entityID `json:"kb_id"`
	DocID    entityID `json:"doc_id"`
	ChunkID  entityID `json:"chunk_id"`
	KBName   string   `json:"kb_name"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	ToolName string   `json:"tool_name"`
}

func runtimeSourcePayload(raw string, expected []runtimeKnowledgeSource) error {
	var payload struct {
		Sources []runtimeKnowledgeSource `json:"kb_sources"`
		Tools   []string                 `json:"tool_names"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Errorf("p7.sources.raw: %w", err)
	}
	if !sameKnowledgeSources(payload.Sources, expected, true) || !slices.Contains(payload.Tools, "fixture_echo") {
		return errors.New("p7.sources.raw: 持久来源不是 Retrieve/WS 同一 UUID 对象，或缺少网络工具")
	}
	return nil
}

func sameKnowledgeSources(actual, expected []runtimeKnowledgeSource, includeContent bool) bool {
	if len(actual) != len(expected) {
		return false
	}
	return !slices.ContainsFunc(expected, func(want runtimeKnowledgeSource) bool {
		return !slices.ContainsFunc(actual, func(got runtimeKnowledgeSource) bool {
			return got.Type == want.Type && got.KBID == want.KBID && got.DocID == want.DocID && got.ChunkID == want.ChunkID && (!includeContent || got.Content == want.Content)
		})
	})
}

// A fresh WS connection per exchange ensures no earlier event satisfies a probe.
// The fixture can answer only from AIM's retrieved knowledge context,
// never from previous chat text or fixture-side persistence.
func (d *driver) runtimeCompositionExchange(caller account, address string, botID, convID entityID, text string, sources []runtimeKnowledgeSource) (string, error) {
	socket, err := d.p6Connect(address, caller)
	if err != nil {
		return "", err
	}
	defer socket.close()
	sent, err := d.sendBotMention(caller, botID, convID, text)
	if err != nil {
		return "", err
	}
	evt, err := d.p6Wait(socket, 0, "knowledge.reply", func(e p6Event) bool {
		return e.Type == "message.new" && hasEntityID(e.ConvID, convID) && hasEntityID(e.Message.SenderID, botID) && hasEntityID(e.Message.ReplyToID, sent.MessageID)
	})
	if err != nil {
		return "", err
	}
	msg, err := d.identityMessage(caller, evt.Message.MessageID)
	if err != nil {
		return "", err
	}
	if msg.SenderType != "bot" || !hasEntityID(msg.SenderID, botID) || !hasEntityID(msg.RequesterID, caller.id) || msg.Text.Text != evt.Message.Content.Text {
		return "", errors.New("p7.knowledge: WS与真实历史身份正文失配")
	}
	reply := sentMessage{MessageID: msg.MessageID}
	reply.Content.Text = msg.Text.Text
	if err := d.p6BotStream(socket, caller, botID, convID, sent.MessageID, reply); err != nil {
		return "", err
	}
	return msg.Text.Text, runtimeSourcePayload(msg.RawPayload, sources)
}

func (d *driver) runtimeKnowledgeComposition(owner, outsider account, address string, mcpID, embeddingID entityID, suffix string) (result error) {
	chatName := "fixture-composition-" + suffix
	chatID, err := d.fixtureModel(owner, "chat", chatName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+chatID.String(), owner.token, nil, nil))
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
			"response_triggers": []string{"mention"}, "streaming_enabled": true, "enable_knowledge": true,
		}, &bot); err != nil {
			return err
		}
		if bot.ID == "" || !hasEntityID(bot.OwnerID, owner.id) || !hasEntityID(bot.ModelID, chatID) {
			return errors.New("p7.bot.config: 知识问答Bot所有权或模型引用身份不匹配")
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
	for _, botID := range bots {
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
		if err := d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", owner.token, map[string]any{"bot_id": botID}, nil); err != nil {
			return err
		}
	}
	if err := d.fixtureEchoPolicy(owner, mcpID, convs...); err != nil {
		return err
	}
	var kb struct {
		ID      entityID  `json:"id"`
		OwnerID *entityID `json:"owner_id"`
	}
	if err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
		"name": "p7-composition-" + suffix, "mode": "rag", "embedding_model_id": embeddingID, "embedding_model": "fixture-embed-" + suffix,
		"pipeline_config": map[string]any{"parsing": map[string]any{"engines": []string{"builtin"}}, "chunking": map[string]any{"chunk_size": 1024, "overlap": 0}, "retrieval": map[string]any{"mode": "vector", "candidate_top_k": 10, "score_threshold": 0.7}},
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
		if _, err := d.resourceGrant(owner, bots[0], convs[0], "knowledge", kb.ID, "conversation", true); err != nil {
			return err
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
	isolationReply, err := d.runtimeCompositionExchange(owner, address, bots[1], convs[1], isolationQuery, nil)
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
		if i == 0 {
			if _, err := d.resourceGrant(owner, bots[0], convs[0], "knowledge", kb.ID, "conversation", true); err != nil {
				return err
			}
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
	observed, err := d.observeProvider()
	if err != nil {
		return err
	}
	if !slices.Contains(observed.ToolCallIDs, "fixture-call") {
		return errors.New("p7.provider: 未观测真实网络tool-call原值")
	}
	fmt.Printf("P7 evidence: bot_id=%s user_id=%s conv=%s; knowledge kb=%s doc=%s chunk=%s; multipart上传/检索、Bot与会话绑定、WS/SSE/history来源与解绑、同名Bot知识隔离和网络tool-call ID PASS\n", bots[0], owner.id, convs[0], kb.ID, doc.ID, chunks.Items[0].ID)
	return nil
}

// An explicit operation uses the same durable Run contract as a mention.
func (d *driver) runtimeCompositionStream(caller account, botID, convID entityID, text string, expected []runtimeKnowledgeSource) error {
	var grants []resourceAuthorization
	if err := d.request(http.MethodPost, "/bot-authorizations/resources", caller.token, map[string]any{"op": "list", "bot_id": botID, "conv_id": convID}, &grants); err != nil {
		return err
	}
	ids := make([]entityID, 0, len(grants))
	for _, grant := range grants {
		if grant.CanShare && grant.ResourceType == "knowledge" {
			ids = append(ids, grant.ID)
		}
	}
	accepted, err := d.interactionStart(caller, botID, convID, text, map[string]any{"resource_grant_ids": ids})
	run := accepted.publicRun
	if err != nil {
		return err
	}
	run, err = d.waitRun(caller, run.ID)
	if err != nil {
		return err
	}
	publicSources := slices.Clone(expected)
	for i := range publicSources {
		publicSources[i].Content = ""
	}
	knowledgeSources := make([]runtimeKnowledgeSource, 0)
	for _, source := range run.Sources {
		if source.Type == "rag" {
			knowledgeSources = append(knowledgeSources, source)
		}
	}
	if len(publicSources) == 0 {
		publicSources = []runtimeKnowledgeSource{}
	}
	if run.Status != "succeeded" || run.OutputStatus != "committed" || run.MessageID == nil || run.ResultText != "fixture-reply:"+text+"|knowledge:cobalt-47" || !sameKnowledgeSources(knowledgeSources, publicSources, false) {
		return errors.New("p7.run: 增强正文或来源未还原真实知识身份")
	}
	msg, err := d.identityMessage(caller, *run.MessageID)
	if err != nil {
		return err
	}
	if msg.SenderType != "bot" || !hasEntityID(msg.SenderID, botID) || !hasEntityID(msg.RequesterID, caller.id) || msg.Text.Text != run.ResultText {
		return errors.New("p7.run: 执行结果与真实消息身份正文失配")
	}
	return runtimeSourcePayload(msg.RawPayload, expected)
}
