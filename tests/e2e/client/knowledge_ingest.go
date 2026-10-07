package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const knownKnowledge = "P5_KNOWN_ANCHOR: the launch code is cobalt-47; indexed knowledge stays available."

var errKnownUnavailable = errors.New("knowledge.search: 未命中指定KB中已入库的精确文档内容")

type documentView struct {
	ID     entityID `json:"id"`
	KBID   entityID `json:"kb_id"`
	Title  string   `json:"title"`
	Status string   `json:"status"`
	Chunks int      `json:"chunk_count"`
	Error  string   `json:"error_message"`
	Stages []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"stages"`
}

func (d *driver) uploadKnowledge(owner account, kbID entityID, filename, title, content string) (documentView, error) {
	var doc documentView
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("title", title); err != nil {
		return doc, err
	}
	file, err := form.CreateFormFile("file", filename)
	if err != nil {
		return doc, err
	}
	if _, err := io.WriteString(file, content); err != nil {
		return doc, err
	}
	if err := form.Close(); err != nil {
		return doc, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()
	path := "/knowledge/bases/" + kbID.String() + "/documents"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.gateway+"/api/v1"+path, &body)
	if err != nil {
		return doc, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+owner.token)
	resp, err := d.client.Do(req)
	if err != nil {
		return doc, fmt.Errorf("knowledge.upload: %s", transportFailure(err))
	}
	defer resp.Body.Close()
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return doc, errors.New("knowledge.upload: 响应不是契约JSON")
	}
	if envelope.Code == nil {
		return doc, errors.New("knowledge.upload: 缺少业务code")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || *envelope.Code != 0 {
		return doc, &apiError{method: http.MethodPost, path: path, status: resp.StatusCode, code: *envelope.Code}
	}
	if err := json.Unmarshal(envelope.Data, &doc); err != nil {
		return doc, errors.New("knowledge.upload: 无效文档JSON")
	}
	if doc.ID == "" || doc.KBID != kbID || doc.Title != title || doc.Status != "pending" {
		return doc, errors.New("knowledge.upload: 上传未返回指定KB/标题的pending异步文档")
	}
	return doc, nil
}

func (d *driver) awaitKnowledgeDocument(owner account, expected documentView, status string, deadline time.Time) error {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("knowledge.ingest: doc_id=%s 未在有界时间内到达%s", expected.ID, status)
		}
		bounded := *d
		bounded.timeout = min(d.timeout, remaining)
		var actual documentView
		if err := bounded.request(http.MethodGet, "/knowledge/documents/"+expected.ID.String(), owner.token, nil, &actual); err != nil {
			return err
		}
		if actual.ID != expected.ID || actual.KBID != expected.KBID || actual.Title != expected.Title {
			return errors.New("knowledge.ingest: 返回文档身份不匹配")
		}
		if actual.Status == "ready" || actual.Status == "failed" {
			if actual.Status != status {
				return fmt.Errorf("knowledge.ingest: doc_id=%s 终态%s，期望%s", actual.ID, actual.Status, status)
			}
			if status == "ready" && actual.Chunks <= 0 {
				return errors.New("knowledge.ingest: ready文档没有索引chunks")
			}
			if status == "ready" {
				completed := make(map[string]bool)
				for _, stage := range actual.Stages {
					completed[stage.Name] = stage.Status == "ready"
				}
				if !completed["parsing"] || !completed["chunking"] || !completed["embedding"] {
					return errors.New("knowledge.ingest: ready文档未持久化完整解析/分块/向量化阶段")
				}
			}
			if status == "failed" && actual.Error == "" {
				return errors.New("knowledge.ingest: failed文档缺少失败原因")
			}
			return nil
		}
		time.Sleep(min(200*time.Millisecond, time.Until(deadline)))
	}
}

func (d *driver) searchKnownKnowledge(owner account, kbID entityID, docID entityID, title string) error {
	bounded := *d
	bounded.timeout = d.queryDeadline
	var response struct {
		Items []struct {
			ChunkID entityID `json:"chunk_id"`
			DocID   entityID `json:"doc_id"`
			Content string   `json:"content"`
			KBID    entityID `json:"kb_id"`
			Title   string   `json:"doc_title"`
		} `json:"items"`
	}
	started := time.Now()
	if err := bounded.request(http.MethodPost, "/knowledge/bases/"+kbID.String()+"/search", owner.token,
		map[string]any{"query": "P5_KNOWN_ANCHOR launch code", "kb_ids": []entityID{kbID}}, &response); err != nil {
		return err
	}
	if time.Since(started) > d.queryDeadline {
		return errors.New("knowledge.search: 查询超过硬deadline")
	}
	for _, item := range response.Items {
		if item.KBID != kbID {
			return errors.New("knowledge.search: 返回了其它KB的索引数据")
		}
		if item.Content == knownKnowledge && item.Title == title && item.DocID == docID && item.ChunkID != "" {
			return nil
		}
	}
	return errKnownUnavailable
}

func (d *driver) knowledgeIngest() (result error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("knowledge", suffix)
	if err != nil {
		return err
	}
	modelName := "fixture-embed-" + suffix
	modelID, err := d.fixtureModel(owner, "embed", modelName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+modelID.String(), owner.token, nil, nil))
	}()
	var kb struct {
		OwnerType string    `json:"owner_type"`
		ID        entityID  `json:"id"`
		OwnerID   *entityID `json:"owner_id"`
		ModelID   *entityID `json:"embedding_model_id"`
	}
	if err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
		"name": "p5-knowledge-" + suffix, "mode": "rag", "embedding_model": modelName, "embedding_model_id": modelID,
		"pipeline_config": map[string]any{
			"parsing":   map[string]any{"engines": []string{"builtin"}},
			"chunking":  map[string]any{"chunk_size": 1024, "overlap": 0},
			"retrieval": map[string]any{"mode": "vector", "top_k": 5, "candidate_top_k": 10, "score_threshold": 0.7},
		},
	}, &kb); err != nil {
		return err
	}
	if kb.ID == "" || kb.OwnerType != "user" || !hasEntityID(kb.OwnerID, owner.id) || !hasEntityID(kb.ModelID, modelID) {
		return errors.New("knowledge.create: KB身份/embedding模型不匹配")
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/knowledge/bases/"+kb.ID.String(), owner.token, nil, nil))
	}()
	title := "p5-known-" + suffix
	known, err := d.uploadKnowledge(owner, kb.ID, "known.txt", title, knownKnowledge)
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, known, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	// Milvus visibility is asynchronous after the pipeline marks a document ready.
	visibility := time.Now().Add(d.timeout)
	for {
		err := d.searchKnownKnowledge(owner, kb.ID, known.ID, title)
		if err == nil {
			break
		}
		if !errors.Is(err, errKnownUnavailable) || !time.Now().Before(visibility) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	observed, err := d.observeProvider()
	if err != nil {
		return err
	}
	if !slices.Contains(observed.EmbedModels, modelName) {
		return errors.New("knowledge.provider: 未观测到gateway registry模型的真实HTTP embedding调用")
	}

	type uploaded struct {
		doc documentView
		err error
	}
	completed := make(chan uploaded, 4)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range 4 {
		workers.Go(func() {
			<-start
			// Four distinct documents of over 256 KiB; every chunk reaches a
			// deliberately slow external embedding provider, not an AIM mock.
			line := fmt.Sprintf("P5_BULK_INGEST document=%d suffix=%s The orbital archive has unrelated background material.\n", i, suffix)
			content := strings.Repeat(line, 3072)
			doc, err := d.uploadKnowledge(owner, kb.ID, fmt.Sprintf("bulk-%d.txt", i), fmt.Sprintf("p5-bulk-%d-%s", i, suffix), content)
			completed <- uploaded{doc: doc, err: err}
		})
	}
	// Join before deferred KB deletion, including failure paths.
	defer workers.Wait()
	close(start)
	deadline := time.Now().Add(d.ingestTimeout)
	var documents []documentView
	overlapQueries := 0
	for len(documents) < 4 || overlapQueries < 3 {
		if !time.Now().Before(deadline) {
			return errors.New("knowledge.concurrent: 未观测到四个并发上传及三次真实入库窗口内的正确查询")
		}
		for {
			select {
			case finished := <-completed:
				if finished.err != nil {
					return finished.err
				}
				documents = append(documents, finished.doc)
			default:
				goto drained
			}
		}
	drained:
		before, err := d.observeProvider()
		if err != nil {
			return err
		}
		if err := d.searchKnownKnowledge(owner, kb.ID, known.ID, title); err != nil {
			return fmt.Errorf("knowledge.concurrent.query: %w", err)
		}
		after, err := d.observeProvider()
		if err != nil {
			return err
		}
		if before.ActiveIngest > 0 && after.ActiveIngest > 0 {
			overlapQueries++
			fmt.Printf("E2E evidence: knowledge concurrent query=%d deadline=%s active_external_ingest=%d\n", overlapQueries, d.queryDeadline, after.ActiveIngest)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, doc := range documents {
		if err := d.awaitKnowledgeDocument(owner, doc, "ready", deadline); err != nil {
			return err
		}
	}
	failed, err := d.uploadKnowledge(owner, kb.ID, "rejected.txt", "p5-rejected-"+suffix, "P5_FAIL_INGEST external provider rejects this document "+suffix)
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, failed, "failed", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	for range 3 {
		if err := d.searchKnownKnowledge(owner, kb.ID, known.ID, title); err != nil {
			return fmt.Errorf("knowledge.after-failure.query: %w", err)
		}
	}
	return d.knowledgeLifecycle(owner, kb.ID, modelID, known, failed, suffix)
}

type knowledgeChunkView struct {
	ID       entityID `json:"id"`
	DocID    entityID `json:"doc_id"`
	Content  string   `json:"content"`
	Metadata string   `json:"metadata"`
}

func (d *driver) knowledgeLifecycle(owner account, kbID, modelID entityID, known, failed documentView, suffix string) (result error) {
	basePath := "/knowledge/bases/" + kbID.String()
	docPath := "/knowledge/documents/" + known.ID.String()
	outsider, err := d.register("knowledge-outsider", suffix)
	if err != nil {
		return err
	}
	for _, path := range []string{basePath, basePath + "/documents", docPath, docPath + "/content", docPath + "/chunks"} {
		if err := d.runtimeReject(http.MethodGet, path, outsider, nil, http.StatusForbidden); err != nil {
			return err
		}
	}
	for _, step := range []struct {
		method, path string
		input        any
	}{
		{http.MethodPost, basePath + "/search", map[string]string{"query": "P5_KNOWN_ANCHOR"}},
		{http.MethodDelete, docPath, nil},
		{http.MethodPost, "/knowledge/documents/" + failed.ID.String() + "/retry", nil},
	} {
		if err := d.runtimeReject(step.method, step.path, outsider, step.input, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err := d.runtimeReject(http.MethodPost, "/knowledge/bases", outsider, map[string]string{"name": "forbidden-platform", "owner_type": "platform"}, http.StatusBadRequest); err != nil {
		return err
	}
	if _, err := d.uploadKnowledge(outsider, kbID, "foreign.txt", "foreign", "forbidden content"); err == nil {
		return errors.New("knowledge.permission: 其它用户上传被允许")
	} else {
		var rejected *apiError
		if !errors.As(err, &rejected) || rejected.status != http.StatusForbidden {
			return fmt.Errorf("knowledge.permission.upload: %w", err)
		}
	}
	var original struct {
		Content string `json:"content"`
	}
	if err := d.request(http.MethodGet, docPath+"/content", owner.token, nil, &original); err != nil {
		return err
	}
	if original.Content != knownKnowledge {
		return errors.New("knowledge.content: 对象存储未还原同一文档内容")
	}
	var chunks struct {
		Items []knowledgeChunkView `json:"chunks"`
	}
	if err := d.request(http.MethodGet, docPath+"/chunks", owner.token, nil, &chunks); err != nil {
		return err
	}
	if len(chunks.Items) != 1 || chunks.Items[0].DocID != known.ID || chunks.Items[0].Content != knownKnowledge {
		return errors.New("knowledge.chunks: 关系片段未还原同一文档内容")
	}
	var sources struct {
		Items []struct {
			ChunkID entityID `json:"chunk_id"`
			DocID   entityID `json:"doc_id"`
			KBID    entityID `json:"kb_id"`
		} `json:"items"`
	}
	if err := d.request(http.MethodPost, basePath+"/search", owner.token, map[string]string{"query": "P5_KNOWN_ANCHOR"}, &sources); err != nil {
		return err
	}
	if len(sources.Items) != 1 || sources.Items[0].ChunkID != chunks.Items[0].ID || sources.Items[0].DocID != known.ID || sources.Items[0].KBID != kbID {
		return errors.New("knowledge.sources: 检索主键不是原关系chunk UUID")
	}
	var config struct {
		ModelID *entityID `json:"embedding_model_id"`
	}
	if err := d.request(http.MethodPut, basePath, owner.token, map[string]string{"description": "preserve model"}, &config); err != nil {
		return err
	}
	if !hasEntityID(config.ModelID, modelID) {
		return errors.New("knowledge.model: 省略引用意外改变模型")
	}
	config.ModelID = nil
	if err := d.request(http.MethodPut, basePath, owner.token, map[string]bool{"clear_embedding_model_id": true}, &config); err != nil {
		return err
	}
	if config.ModelID != nil {
		return errors.New("knowledge.model: 主动清除未解除模型引用")
	}
	recoveryID, err := d.fixtureModel(owner, "embed", "fixture-embed-recover-"+suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+recoveryID.String(), owner.token, nil, nil))
	}()
	if err := d.request(http.MethodPut, basePath, owner.token, map[string]any{"embedding_model_id": recoveryID}, &config); err != nil {
		return err
	}
	if !hasEntityID(config.ModelID, recoveryID) {
		return errors.New("knowledge.model: 配置未引用正确模型UUID")
	}
	var retried documentView
	if err := d.request(http.MethodPost, "/knowledge/documents/"+failed.ID.String()+"/retry", owner.token, nil, &retried); err != nil {
		return err
	}
	if retried.ID != failed.ID || retried.KBID != kbID {
		return errors.New("knowledge.retry: 重试改变了文档身份")
	}
	if err := d.awaitKnowledgeDocument(owner, failed, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, docPath, owner.token, nil, nil); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodGet, docPath, owner, nil, http.StatusNotFound); err != nil {
		return err
	}
	if err := d.request(http.MethodPost, basePath+"/search", owner.token, map[string]string{"query": "P5_KNOWN_ANCHOR"}, &sources); err != nil {
		return err
	}
	for _, item := range sources.Items {
		if item.DocID == known.ID || item.ChunkID == chunks.Items[0].ID {
			return errors.New("knowledge.delete: 文档删除后旧向量仍可检索")
		}
	}
	if err := d.knowledgeParentLifecycle(owner, modelID, suffix); err != nil {
		return err
	}
	if err := d.knowledgeModelPipeline(owner, modelID, suffix); err != nil {
		return err
	}
	fmt.Println("E2E evidence: knowledge UUID sources/object roundtrip, private ownership, model preserve/clear, failed retry and document deletion PASS")
	return nil
}

func (d *driver) knowledgeParentLifecycle(owner account, modelID entityID, suffix string) (result error) {
	var kb struct {
		ID entityID `json:"id"`
	}
	if err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
		"name": "parent-child-" + suffix, "embedding_model_id": modelID,
		"pipeline_config": map[string]any{
			"chunking":  map[string]any{"overlap": 0, "separators": []string{" "}, "parent_child": map[string]any{"enabled": true, "parent_size": 512, "child_size": 64}},
			"retrieval": map[string]any{"mode": "vector", "top_k": 5, "score_threshold": 0.7},
		},
	}, &kb); err != nil {
		return err
	}
	basePath := "/knowledge/bases/" + kb.ID.String()
	deleted := false
	defer func() {
		if !deleted {
			result = errors.Join(result, d.request(http.MethodDelete, basePath, owner.token, nil, nil))
		}
	}()
	text := strings.Repeat(knownKnowledge+" ", 8)
	doc, err := d.uploadKnowledge(owner, kb.ID, "parent.txt", "parent-"+suffix, text)
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, doc, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	var chunks struct {
		Items []knowledgeChunkView `json:"chunks"`
	}
	if err := d.request(http.MethodGet, "/knowledge/documents/"+doc.ID.String()+"/chunks?limit=100", owner.token, nil, &chunks); err != nil {
		return err
	}
	byID := make(map[entityID]knowledgeChunkView)
	for _, ch := range chunks.Items {
		var meta struct {
			Parent *entityID `json:"parent_chunk_id"`
		}
		if err := json.Unmarshal([]byte(ch.Metadata), &meta); err != nil || meta.Parent == nil || ch.DocID != doc.ID {
			return errors.New("knowledge.parent: 子片段缺少正确父UUID或文档引用")
		}
		byID[ch.ID] = ch
	}
	var sources struct {
		Items []struct {
			ChunkID entityID `json:"chunk_id"`
			DocID   entityID `json:"doc_id"`
			KBID    entityID `json:"kb_id"`
			Content string   `json:"content"`
			Matched string   `json:"matched_content"`
		} `json:"items"`
	}
	if err := d.request(http.MethodPost, basePath+"/search", owner.token, map[string]string{"query": "P5_KNOWN_ANCHOR"}, &sources); err != nil {
		return err
	}
	if len(sources.Items) == 0 {
		return errors.New("knowledge.parent: 未恢复匹配父片段")
	}
	for _, source := range sources.Items {
		child, ok := byID[source.ChunkID]
		if !ok || source.DocID != doc.ID || source.KBID != kb.ID || source.Matched != child.Content || len(source.Content) <= len(source.Matched) || !strings.Contains(source.Content, "cobalt-47") {
			return errors.New("knowledge.parent: 来源未还原同一父子片段内容")
		}
	}
	if err := d.request(http.MethodDelete, basePath, owner.token, nil, nil); err != nil {
		return err
	}
	deleted = true
	if err := d.runtimeReject(http.MethodGet, "/knowledge/documents/"+doc.ID.String(), owner, nil, http.StatusNotFound); err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodPost, basePath+"/search", owner, map[string]string{"query": "P5_KNOWN_ANCHOR"}, http.StatusNotFound); err != nil {
		return err
	}
	fmt.Println("E2E evidence: parent-child UUID recovery and knowledge-base deletion PASS")
	return nil
}

func (d *driver) knowledgeModelPipeline(owner account, embeddingID entityID, suffix string) (result error) {
	vlmName, rerankName := "fixture-vlm-knowledge-"+suffix, "fixture-rerank-knowledge-"+suffix
	vlmID, err := d.fixtureModel(owner, "vlm", vlmName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+vlmID.String(), owner.token, nil, nil))
	}()
	rerankID, err := d.fixtureModel(owner, "rerank", rerankName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+rerankID.String(), owner.token, nil, nil))
	}()
	var kb struct {
		ID entityID `json:"id"`
	}
	if err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
		"name": "model-pipeline-" + suffix, "embedding_model_id": embeddingID,
		"pipeline_config": map[string]any{
			"parsing":   map[string]any{"engines": []string{"builtin"}, "vlm": map[string]any{"enabled": true, "model_id": vlmID}},
			"chunking":  map[string]any{"chunk_size": 1024, "overlap": 0},
			"retrieval": map[string]any{"mode": "vector", "top_k": 1, "candidate_top_k": 10, "score_threshold": 0.7, "rerank": map[string]any{"enabled": true, "model_id": rerankID, "top_n": 1}},
		},
	}, &kb); err != nil {
		return err
	}
	path := "/knowledge/bases/" + kb.ID.String()
	defer func() { result = errors.Join(result, d.request(http.MethodDelete, path, owner.token, nil, nil)) }()
	known, err := d.uploadKnowledge(owner, kb.ID, "known.txt", "rerank exact", knownKnowledge)
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, known, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	vision, err := d.uploadKnowledge(owner, kb.ID, "vision.md", "vision image", "P5_KNOWN_ANCHOR image description\n\n![pixel]("+d.provider+"/knowledge.png)")
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, vision, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	var chunks struct {
		Items []knowledgeChunkView `json:"chunks"`
	}
	if err := d.request(http.MethodGet, "/knowledge/documents/"+vision.ID.String()+"/chunks", owner.token, nil, &chunks); err != nil {
		return err
	}
	transcribed := false
	for _, ch := range chunks.Items {
		if ch.DocID == vision.ID && strings.Contains(ch.Content, "fixture-vision:Please describe") {
			transcribed = true
		}
	}
	if !transcribed {
		return errors.New("knowledge.vlm: 实际图片转写未进入文档关系片段")
	}
	var sources struct {
		Items []struct {
			DocID   entityID `json:"doc_id"`
			Content string   `json:"content"`
		} `json:"items"`
	}
	if err := d.request(http.MethodPost, path+"/search", owner.token, map[string]string{"query": knownKnowledge}, &sources); err != nil {
		return err
	}
	if len(sources.Items) != 1 || sources.Items[0].DocID != known.ID || sources.Items[0].Content != knownKnowledge {
		return errors.New("knowledge.rerank: 真实重排没有返回正确文档来源")
	}
	observed, err := d.observeProvider()
	if err != nil {
		return err
	}
	if !slices.Contains(observed.ChatModels, vlmName) || !slices.Contains(observed.RerankModels, rerankName) {
		return errors.New("knowledge.models: 未观测到知识VLM/rerank UUID配置对应的外部模型调用")
	}
	fmt.Println("E2E evidence: knowledge VLM image transcription and rerank model selection through real local storage PASS")
	return nil
}
