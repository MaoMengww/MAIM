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
	ID     decimal `json:"id"`
	KBID   decimal `json:"kb_id"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Chunks int     `json:"chunk_count"`
	Error  string  `json:"error_message"`
}

func (d *driver) uploadKnowledge(owner account, kbID decimal, filename, title, content string) (documentView, error) {
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
	if doc.ID <= 0 || doc.KBID != kbID || doc.Title != title || doc.Status != "pending" {
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
			if status == "failed" && actual.Error == "" {
				return errors.New("knowledge.ingest: failed文档缺少失败原因")
			}
			return nil
		}
		time.Sleep(min(200*time.Millisecond, time.Until(deadline)))
	}
}

func (d *driver) searchKnownKnowledge(owner account, kbID decimal, title string) error {
	bounded := *d
	bounded.timeout = d.queryDeadline
	var response struct {
		Items []struct {
			Content string  `json:"content"`
			KBID    decimal `json:"kb_id"`
			Title   string  `json:"doc_title"`
		} `json:"items"`
	}
	started := time.Now()
	if err := bounded.request(http.MethodPost, "/knowledge/bases/"+kbID.String()+"/search", owner.token,
		map[string]any{"query": "P5_KNOWN_ANCHOR launch code", "kb_ids": []decimal{kbID}}, &response); err != nil {
		return err
	}
	if time.Since(started) > d.queryDeadline {
		return errors.New("knowledge.search: 查询超过硬deadline")
	}
	for _, item := range response.Items {
		if item.KBID != kbID {
			return errors.New("knowledge.search: 返回了其它KB的索引数据")
		}
		if item.Content == knownKnowledge && item.Title == title {
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
		ID      decimal `json:"id"`
		OwnerID decimal `json:"owner_id"`
		ModelID decimal `json:"embedding_model_id"`
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
	if kb.ID <= 0 || kb.OwnerID != owner.id || kb.ModelID != modelID {
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
		err := d.searchKnownKnowledge(owner, kb.ID, title)
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
		if err := d.searchKnownKnowledge(owner, kb.ID, title); err != nil {
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
		if err := d.searchKnownKnowledge(owner, kb.ID, title); err != nil {
			return fmt.Errorf("knowledge.after-failure.query: %w", err)
		}
	}
	return nil
}
