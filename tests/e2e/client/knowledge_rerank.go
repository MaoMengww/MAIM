package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	kbpb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Focused entry point reuses the existing model pipeline without bulk-ingest load.
func (d *driver) knowledgeRerankScenario() (result error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("knowledge_rerank", suffix)
	if err != nil {
		return err
	}
	embeddingID, err := d.fixtureModel(owner, "embed", "fixture-embed-rerank-"+suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+embeddingID.String(), owner.token, nil, nil))
	}()
	return d.knowledgeModelPipeline(owner, embeddingID, suffix)
}

type knowledgeRerankSource struct {
	ChunkID  entityID `json:"chunk_id"`
	DocID    entityID `json:"doc_id"`
	KBID     entityID `json:"kb_id"`
	Content  string   `json:"content"`
	Title    string   `json:"doc_title"`
	Score    float64  `json:"score"`
	Metadata string   `json:"metadata_json"`
}

type knowledgeRerankStats struct {
	Requests []struct {
		Path      string `json:"path"`
		State     string `json:"state"`
		Candidate string `json:"candidate"`
	} `json:"requests"`
	Completed []string `json:"completed"`
}

func knowledgeRerankConfig(enabled bool, method string, model entityID, topN int) map[string]any {
	return map[string]any{"enabled": enabled, "method": method, "model_id": model, "top_n": topN}
}

func knowledgeRerankUpdate(rerank map[string]any) map[string]any {
	return map[string]any{"pipeline_config": map[string]any{"retrieval": map[string]any{
		"mode": "vector", "candidate_top_k": 20, "score_threshold": .7, "rerank": rerank,
	}}}
}

func (d *driver) knowledgeRerankSearch(owner account, path, query string, limit int) ([]knowledgeRerankSource, error) {
	var response struct {
		Items []knowledgeRerankSource `json:"items"`
	}
	err := d.request(http.MethodPost, path+"/search", owner.token, map[string]any{"query": query, "limit": limit}, &response)
	return response.Items, err
}

func requireKnowledgeRerankSources(actual, expected []knowledgeRerankSource) error {
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("knowledge.rerank: 排序/数量/原召回分数/来源丢失: got=%v want=%v", knowledgeRerankIDs(actual), knowledgeRerankIDs(expected))
	}
	return nil
}

func knowledgeRerankIDs(items []knowledgeRerankSource) []entityID {
	ids := make([]entityID, len(items))
	for i, item := range items {
		ids[i] = item.ChunkID
	}
	return ids
}

func (d *driver) knowledgeRerankStats(model string) (knowledgeRerankStats, error) {
	var stats knowledgeRerankStats
	err := d.executorProvider("/knowledge-rerank/stats", map[string]string{"model": model}, &stats)
	return stats, err
}

func (d *driver) knowledgeRerankArm(model, query, mode string, items []knowledgeRerankSource, scores []float64) error {
	candidates := make(map[string]float64, len(items))
	for i, item := range items {
		candidates[item.Content] = scores[i]
	}
	first := ""
	if len(items) > 0 {
		first = items[0].Content
	}
	return d.executorProvider("/knowledge-rerank/arm", map[string]any{
		"model": model, "query": query, "mode": mode, "candidates": candidates, "first": first,
	}, nil)
}

func (d *driver) requireKnowledgeRerankCalls(model string, count int, state string) error {
	stats, err := d.knowledgeRerankStats(model)
	if err != nil {
		return err
	}
	if len(stats.Requests) != count {
		return fmt.Errorf("knowledge.rerank: model=%s 供应商调用=%d，期望%d", model, len(stats.Requests), count)
	}
	for _, row := range stats.Requests {
		if state != "" && row.State != state {
			return fmt.Errorf("knowledge.rerank: 供应商状态=%s，期望%s", row.State, state)
		}
	}
	return nil
}

// Continues the existing upload -> real storage -> Retrieve -> provider journey.
// Only the provider's native HTTP responses and completion gates are controlled.
func (d *driver) knowledgeDualReranking(owner account, embeddingID, kbID entityID, known, vision documentView, suffix string) (result error) {
	path := "/knowledge/bases/" + kbID.String()
	if err := d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(map[string]any{"enabled": false, "method": "rerank"}), nil); err != nil {
		return err
	}
	query := "P5_KNOWN_ANCHOR evidence lookup " + suffix
	judgeName, nativeName := "fixture-judge-knowledge-"+suffix, "fixture-native-knowledge-"+suffix
	var judge struct {
		ID entityID `json:"id"`
	}
	judgeConnection, err := d.fixtureConnection(owner, judgeName, "typesafe", d.provider+"/native", componentKey)
	if err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/models", owner.token, map[string]any{
		"model_name": judgeName, "connection_id": judgeConnection, "capability": "judge", "input_price_per_mtok": 2,
	}, &judge); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+judge.ID.String(), owner.token, nil, nil))
	}()
	nativeID, err := d.fixtureModel(owner, "rerank", nativeName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+nativeID.String(), owner.token, nil, nil))
	}()
	if err := d.updateFixtureConnection(owner, nativeID,
		map[string]any{"provider": "cohere", "base_url": d.provider + "/native"}); err != nil {
		return err
	}
	var disabled struct {
		Pipeline struct {
			Retrieval struct {
				Rerank struct {
					Enabled bool `json:"enabled"`
				} `json:"rerank"`
			} `json:"retrieval"`
		} `json:"pipeline_config"`
	}
	if err := d.request(http.MethodGet, path, owner.token, nil, &disabled); err != nil {
		return err
	}
	if disabled.Pipeline.Retrieval.Rerank.Enabled {
		return errors.New("knowledge.rerank: 注册新模型自动开启重排")
	}
	update := func(enabled bool, method string, id entityID, topN int) error {
		return d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(knowledgeRerankConfig(enabled, method, id, topN)), nil)
	}
	// Registering a model must not activate a disabled knowledge configuration.
	if err := update(false, "judge", judge.ID, 10); err != nil {
		return err
	}
	if err := d.knowledgeRerankArm(judgeName, query, "ordered", nil, nil); err != nil {
		return err
	}
	if err := d.knowledgeRerankArm(nativeName, query, "ordered", nil, nil); err != nil {
		return err
	}
	var baseline []knowledgeRerankSource
	if err := d.broadcastPoll("knowledge.rerank.two-visible", func(bounded *driver) (bool, error) {
		items, err := bounded.knowledgeRerankSearch(owner, path, query, 10)
		baseline = items
		return len(items) == 2, err
	}); err != nil {
		return err
	}
	for _, item := range baseline {
		if item.KBID != kbID || item.ChunkID == "" || item.Title == "" || item.Score < .7 || (item.DocID != known.ID && item.DocID != vision.ID) {
			return errors.New("knowledge.rerank: 未取得真实两文档的完整召回来源")
		}
	}
	if baseline[0].DocID == baseline[1].DocID {
		return errors.New("knowledge.rerank: 两候选不是两个实际上传文档")
	}
	for _, name := range []string{judgeName, nativeName} {
		if err := d.requireKnowledgeRerankCalls(name, 0, ""); err != nil {
			return err
		}
	}
	// Higher score is deliberately assigned to the second original candidate.
	reversed := []knowledgeRerankSource{baseline[1], baseline[0]}
	scores := []float64{.05, .95}
	if err := d.knowledgeRerankArm(nativeName, query, "ordered", baseline, scores); err != nil {
		return err
	}
	if err := update(true, "rerank", nativeID, 10); err != nil {
		return err
	}
	items, err := d.knowledgeRerankSearch(owner, path, query, 10)
	if err != nil {
		return err
	}
	if err := requireKnowledgeRerankSources(items, reversed); err != nil {
		return err
	}
	if err := d.requireKnowledgeRerankCalls(nativeName, 1, "completed"); err != nil {
		return err
	}
	if err := d.requireKnowledgeRerankCalls(judgeName, 0, ""); err != nil {
		return err
	}
	if err := d.knowledgeRerankArm(nativeName, query, "ordered", nil, nil); err != nil {
		return err
	}
	if err := update(true, "judge", judge.ID, 10); err != nil {
		return err
	}
	if err := d.knowledgeRerankConfiguration(owner, path, judge.ID, nativeID, embeddingID, suffix); err != nil {
		return err
	}
	for _, scenario := range []struct {
		name        string
		scores      []float64
		topN, limit int
		want        []knowledgeRerankSource
	}{
		{"reverse", scores, 10, 10, reversed},
		{"tie", []float64{.4, .4}, 10, 10, baseline},
		{"low", []float64{.0001, .0002}, 10, 10, reversed},
		{"ordered", scores, 1, 10, reversed[:1]},
		{"ordered", scores, 10, 1, reversed[:1]},
		{"http_error", scores, 10, 10, baseline},
		{"malformed", scores, 10, 10, baseline},
		{"missing", scores, 10, 10, baseline},
		{"wrong_type", scores, 10, 10, baseline},
		{"probability", scores, 10, 10, baseline},
		{"mapping", scores, 10, 10, baseline},
	} {
		if err := update(true, "judge", judge.ID, scenario.topN); err != nil {
			return err
		}
		if err := d.knowledgeRerankArm(judgeName, query, scenario.name, baseline, scenario.scores); err != nil {
			return err
		}
		beforeBills, err := d.knowledgeJudgeBilling(owner, judge.ID, judgeName)
		if err != nil {
			return err
		}
		items, err := d.knowledgeRerankSearch(owner, path, query, scenario.limit)
		if err != nil {
			return fmt.Errorf("knowledge.judge.%s: %w", scenario.name, err)
		}
		if err := requireKnowledgeRerankSources(items, scenario.want); err != nil {
			return fmt.Errorf("knowledge.judge.%s: %w", scenario.name, err)
		}
		if err := d.requireKnowledgeRerankCalls(judgeName, 1, ""); err != nil {
			return err
		}
		stats, err := d.knowledgeRerankStats(judgeName)
		if err != nil {
			return err
		}
		for _, row := range stats.Requests {
			if row.Path != "/native/v1/systemone" || row.State == "invalid_request" || row.State == "gate_timeout" {
				return errors.New("knowledge.judge: 没有使用有效的共享 state 判断协议")
			}
		}
		if scenario.name == "reverse" && len(stats.Completed) != 2 {
			return errors.New("knowledge.judge: 单批没有包含两个候选")
		}
		if scenario.name == "http_error" {
			// Shared-state failure is whole-request: the provider returned no usage,
			// so the fallback must not invent a billing record.
			if err := d.broadcastPoll("knowledge.judge.fallback-billing", func(bounded *driver) (bool, error) {
				afterBills, err := bounded.knowledgeJudgeBilling(owner, judge.ID, judgeName)
				return afterBills == beforeBills, err
			}); err != nil {
				return err
			}
		}
		if scenario.name == "missing" {
			// A malformed answer still carries provider usage; the consumed call
			// must remain billable even though the whole ranking falls back.
			if err := d.broadcastPoll("knowledge.judge.partial-usage-billing", func(bounded *driver) (bool, error) {
				afterBills, err := bounded.knowledgeJudgeBilling(owner, judge.ID, judgeName)
				return afterBills == beforeBills+1, err
			}); err != nil {
				return err
			}
		}
		if err := d.requireKnowledgeRerankCalls(nativeName, 0, ""); err != nil {
			return err
		}
		fmt.Printf("E2E evidence: knowledge dual-rerank scenario=%s limit=%d top_n=%d candidates=2 PASS\n", scenario.name, scenario.limit, scenario.topN)
	}
	// A real successful lookup immediately after failed scoring proves no poisoned state.
	if err := update(true, "judge", judge.ID, 10); err != nil {
		return err
	}
	if err := d.knowledgeRerankArm(judgeName, query, "ordered", baseline, scores); err != nil {
		return err
	}
	items, err = d.knowledgeRerankSearch(owner, path, query, 10)
	if err != nil {
		return err
	}
	if err := requireKnowledgeRerankSources(items, reversed); err != nil {
		return err
	}
	if err := d.knowledgeRerankArm(judgeName, query, "ordered", baseline, scores); err != nil {
		return err
	}
	outsider, err := d.register("rerank_outsider", suffix)
	if err != nil {
		return err
	}
	if err := d.runtimeReject(http.MethodPost, path+"/search", outsider, map[string]any{"query": query}, http.StatusForbidden); err != nil {
		return err
	}
	if err := d.requireKnowledgeRerankCalls(judgeName, 0, ""); err != nil {
		return err
	}
	// Orthogonal external embedding direction produces no eligible candidates.
	items, err = d.knowledgeRerankSearch(owner, path, "unrelated zero-candidate query", 10)
	if err != nil || len(items) != 0 {
		return fmt.Errorf("knowledge.judge.zero: items=%d error=%v", len(items), err)
	}
	if err := d.requireKnowledgeRerankCalls(judgeName, 0, ""); err != nil {
		return err
	}
	if err := update(true, "rerank", nativeID, 10); err != nil {
		return err
	}
	items, err = d.knowledgeRerankSearch(owner, path, "unrelated zero-candidate query", 10)
	if err != nil || len(items) != 0 {
		return fmt.Errorf("knowledge.native.zero: items=%d error=%v", len(items), err)
	}
	if err := d.requireKnowledgeRerankCalls(nativeName, 0, ""); err != nil {
		return err
	}
	if err := update(true, "judge", judge.ID, 10); err != nil {
		return err
	}
	if err := d.knowledgeRerankCancellation(owner, path, kbID, judge.ID, judgeName, query, baseline, suffix); err != nil {
		return err
	}
	// Delete through the real lifecycle; stale vector visibility must not export a removed source.
	if err := d.request(http.MethodDelete, "/knowledge/documents/"+vision.ID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	if err := d.knowledgeRerankArm(judgeName, query, "ordered", baseline, scores); err != nil {
		return err
	}
	items, err = d.knowledgeRerankSearch(owner, path, query, 10)
	if err != nil {
		return err
	}
	var surviving []knowledgeRerankSource
	for _, item := range baseline {
		if item.DocID == known.ID {
			surviving = append(surviving, item)
		}
	}
	if err := requireKnowledgeRerankSources(items, surviving); err != nil {
		return err
	}
	if err := d.requireKnowledgeRerankCalls(judgeName, 0, ""); err != nil {
		return err
	}
	if err := update(true, "rerank", nativeID, 10); err != nil {
		return err
	}
	items, err = d.knowledgeRerankSearch(owner, path, query, 10)
	if err != nil {
		return err
	}
	if err := requireKnowledgeRerankSources(items, surviving); err != nil {
		return err
	}
	if err := d.requireKnowledgeRerankCalls(nativeName, 0, ""); err != nil {
		return err
	}
	if err := update(true, "judge", judge.ID, 10); err != nil {
		return err
	}
	failed, err := d.uploadKnowledge(owner, kbID, "invalid-rerank.txt", "invalid-rerank", "P5_FAIL_INGEST invalid judgment source "+suffix)
	if err != nil {
		return err
	}
	if err := d.awaitKnowledgeDocument(owner, failed, "failed", time.Now().Add(d.ingestTimeout)); err != nil {
		return err
	}
	items, err = d.knowledgeRerankSearch(owner, path, query, 10)
	if err != nil {
		return err
	}
	if err := requireKnowledgeRerankSources(items, surviving); err != nil {
		return err
	}
	if err := d.requireKnowledgeRerankCalls(judgeName, 0, ""); err != nil {
		return err
	}
	fmt.Println("E2E evidence: knowledge dual-rerank off/native/judge; zero/one skip; denied/deleted/failed sources not exported; cancellation/deadline; fallback billing PASS")
	return nil
}

func (d *driver) knowledgeJudgeBilling(owner account, id entityID, name string) (int, error) {
	var records struct {
		Items []struct {
			ModelID      entityID  `json:"model_id"`
			ModelName    string    `json:"model_name"`
			Capability   string    `json:"capability"`
			OwnerID      *entityID `json:"owner_id"`
			InputTokens  *int32    `json:"input_tokens"`
			OutputTokens *int32    `json:"output_tokens"`
			InputCost    float64   `json:"input_cost"`
			OutputCost   float64   `json:"output_cost"`
			TotalCost    float64   `json:"total_cost"`
		} `json:"items"`
	}
	if err := d.request(http.MethodGet, "/models/billing/records?page=1&page_size=100", owner.token, nil, &records); err != nil {
		return 0, err
	}
	count := 0
	for _, row := range records.Items {
		if row.ModelID != id {
			continue
		}
		if row.ModelName != name || row.Capability != "judge" || !hasEntityID(row.OwnerID, owner.id) ||
			!judgeTokenCount(row.InputTokens, true, 13) || !judgeTokenCount(row.OutputTokens, true, 7) ||
			math.Abs(row.InputCost-.000026) > 1e-12 || row.OutputCost != 0 || math.Abs(row.TotalCost-.000026) > 1e-12 {
			return 0, errors.New("knowledge.judge.billing: 真实用量/费用/模型/归属不匹配")
		}
		count++
	}
	return count, nil
}

func (d *driver) knowledgeRerankConfiguration(owner account, path string, judgeID, nativeID, wrongID entityID, suffix string) (result error) {
	var view struct {
		Pipeline struct {
			Retrieval struct {
				Rerank struct {
					Enabled bool      `json:"enabled"`
					Method  string    `json:"method"`
					ModelID *entityID `json:"model_id"`
				} `json:"rerank"`
			} `json:"retrieval"`
		} `json:"pipeline_config"`
	}
	if err := d.request(http.MethodPut, path, owner.token, map[string]string{"description": "preserve selected judge"}, nil); err != nil {
		return err
	}
	if err := d.request(http.MethodGet, path, owner.token, nil, &view); err != nil {
		return err
	}
	rerank := view.Pipeline.Retrieval.Rerank
	if !rerank.Enabled || rerank.Method != "judge" || !hasEntityID(rerank.ModelID, judgeID) {
		return errors.New("knowledge.judge.config: 保存重新读取未保留方式/模型")
	}
	for _, config := range []map[string]any{
		knowledgeRerankConfig(true, "judge", nativeID, 10),
		knowledgeRerankConfig(true, "rerank", judgeID, 10),
		knowledgeRerankConfig(true, "judge", wrongID, 10),
	} {
		if err := d.runtimeReject(http.MethodPut, path, owner, knowledgeRerankUpdate(config), http.StatusConflict); err != nil {
			return err
		}
	}
	if err := d.runtimeReject(http.MethodPut, path, owner, knowledgeRerankUpdate(knowledgeRerankConfig(true, "unsupported", judgeID, 10)), http.StatusBadRequest); err != nil {
		return err
	}
	foreign, err := d.register("rerank_model_owner", suffix)
	if err != nil {
		return err
	}
	var foreignModel struct {
		ID entityID `json:"id"`
	}
	foreignConnection, err := d.fixtureConnection(foreign, "private-judge-"+suffix, "typesafe", d.provider+"/native", componentKey)
	if err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/models", foreign.token, map[string]any{
		"model_name": "fixture-judge-knowledge-private-" + suffix, "connection_id": foreignConnection, "capability": "judge",
	}, &foreignModel); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+foreignModel.ID.String(), foreign.token, nil, nil))
	}()
	if err := d.runtimeReject(http.MethodPut, path, owner, knowledgeRerankUpdate(knowledgeRerankConfig(true, "judge", foreignModel.ID, 10)), http.StatusConflict); err != nil {
		return err
	}
	var expired struct {
		ID entityID `json:"id"`
	}
	expiredConnection, err := d.fixtureConnection(owner, "deleted-judge-"+suffix, "typesafe", d.provider+"/native", componentKey)
	if err != nil {
		return err
	}
	if err := d.request(http.MethodPost, "/models", owner.token, map[string]any{
		"model_name": "fixture-judge-knowledge-deleted-" + suffix, "connection_id": expiredConnection, "capability": "judge",
	}, &expired); err != nil {
		return err
	}
	deleted := false
	defer func() {
		if !deleted {
			result = errors.Join(result, d.request(http.MethodDelete, "/models/"+expired.ID.String(), owner.token, nil, nil))
		}
	}()
	for index, selection := range []struct {
		method     string
		model      entityID
		compatible bool
	}{
		{"rerank", nativeID, false}, {"judge", expired.ID, false}, {"judge", judgeID, true},
	} {
		var other struct {
			ID entityID `json:"id"`
		}
		pipeline := knowledgeRerankUpdate(knowledgeRerankConfig(true, selection.method, selection.model, 10))["pipeline_config"]
		if err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
			"name": fmt.Sprintf("rerank-compat-%d-%s", index, suffix), "embedding_model_id": wrongID, "pipeline_config": pipeline,
		}, &other); err != nil {
			return err
		}
		defer func() {
			result = errors.Join(result, d.request(http.MethodDelete, "/knowledge/bases/"+other.ID.String(), owner.token, nil, nil))
		}()
		body := map[string]any{"query": "unrelated compatibility query", "kb_ids": []string{path[len("/knowledge/bases/"):], other.ID.String()}, "limit": 10}
		if selection.compatible {
			var response struct {
				Items []knowledgeRerankSource `json:"items"`
			}
			if err := d.request(http.MethodPost, "/knowledge/search", owner.token, body, &response); err != nil {
				return err
			}
			if len(response.Items) != 0 {
				return errors.New("knowledge.judge.config: 兼容跨库零候选没有保持召回阈值")
			}
		} else if err := d.runtimeReject(http.MethodPost, "/knowledge/search", owner, body, http.StatusConflict); err != nil {
			return err
		}
	}
	if err := d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(knowledgeRerankConfig(true, "judge", expired.ID, 10)), nil); err != nil {
		return err
	}
	if err := d.request(http.MethodDelete, "/models/"+expired.ID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	deleted = true
	for _, request := range []struct {
		method string
		input  any
	}{
		{http.MethodPut, knowledgeRerankUpdate(knowledgeRerankConfig(true, "judge", expired.ID, 10))},
		{http.MethodPost, map[string]string{"query": "P5_KNOWN_ANCHOR invalid model"}},
	} {
		requestPath := path
		if request.method == http.MethodPost {
			requestPath += "/search"
		}
		err := d.request(request.method, requestPath, owner.token, request.input, nil)
		var rejected *apiError
		if !errors.As(err, &rejected) || rejected.status != http.StatusConflict {
			return fmt.Errorf("knowledge.judge.config: 失效模型未明确拒绝: %v", err)
		}
	}
	// Explicit clearing remains distinct from omission and cannot leave an enabled stale reference.
	if err := d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(map[string]any{"enabled": false, "method": "judge", "clear_model_id": true}), nil); err != nil {
		return err
	}
	view.Pipeline.Retrieval.Rerank.ModelID = nil
	if err := d.request(http.MethodGet, path, owner.token, nil, &view); err != nil {
		return err
	}
	if view.Pipeline.Retrieval.Rerank.ModelID != nil || view.Pipeline.Retrieval.Rerank.Enabled {
		return errors.New("knowledge.judge.config: 主动清除没有解除模型引用")
	}
	return d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(knowledgeRerankConfig(true, "judge", judgeID, 10)), nil)
}

func (d *driver) knowledgeRerankCancellation(owner account, path string, kbID, judgeID entityID, model, query string, baseline []knowledgeRerankSource, suffix string) (result error) {
	// More eligible candidates than the existing ten-call admission pool: held
	// requests expose in-flight and not-yet-started cancellation through HTTP.
	var extra []documentView
	defer func() {
		for _, doc := range extra {
			result = errors.Join(result, d.request(http.MethodDelete, "/knowledge/documents/"+doc.ID.String(), owner.token, nil, nil))
		}
	}()
	for i := range 12 {
		doc, err := d.uploadKnowledge(owner, kbID, fmt.Sprintf("judge-cancel-%d.txt", i), fmt.Sprintf("judge-cancel-%d", i), fmt.Sprintf("P5_KNOWN_ANCHOR cancellation candidate %d %s", i, suffix))
		if err != nil {
			return err
		}
		extra = append(extra, doc)
		if err := d.awaitKnowledgeDocument(owner, doc, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
			return err
		}
	}
	if err := d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(knowledgeRerankConfig(false, "judge", judgeID, 20)), nil); err != nil {
		return err
	}
	var candidates []knowledgeRerankSource
	if err := d.broadcastPoll("knowledge.judge.cancel-candidates", func(bounded *driver) (bool, error) {
		items, err := bounded.knowledgeRerankSearch(owner, path, query, 20)
		candidates = items
		return len(items) == len(baseline)+len(extra), err
	}); err != nil {
		return err
	}
	if err := d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(knowledgeRerankConfig(true, "judge", judgeID, 20)), nil); err != nil {
		return err
	}
	scores := make([]float64, len(candidates))
	for i := range scores {
		scores[i] = .5
	}
	conn, err := grpc.NewClient(d.knowledgeRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	cli := kbpb.NewKnowledgeBaseClient(conn)
	limit := int32(20)
	for _, deadline := range []bool{false, true} {
		if err := d.knowledgeRerankArm(model, query, "hold", candidates, scores); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", owner.id.String()))
		finished := make(chan error, 1)
		go func() {
			_, err := cli.Retrieve(ctx, &kbpb.RetrieveReq{Query: query, KbIds: []string{kbID.String()}, Limit: &limit})
			finished <- err
		}()
		observed := d.broadcastPoll("knowledge.judge.in-flight", func(bounded *driver) (bool, error) {
			stats, err := bounded.knowledgeRerankStats(model)
			return len(stats.Requests) >= 1, err
		})
		if observed != nil {
			cancel()
			<-finished
			return observed
		}
		if !deadline {
			cancel()
		}
		callErr := <-finished
		cancel()
		want := codes.Canceled
		if deadline {
			want = codes.DeadlineExceeded
		}
		if status.Code(callErr) != want {
			return fmt.Errorf("knowledge.judge: 总体取消/截止被成功回退: %v", callErr)
		}
		if err := d.broadcastPoll("knowledge.judge.cancel-propagated", func(bounded *driver) (bool, error) {
			stats, err := bounded.knowledgeRerankStats(model)
			if err != nil {
				return false, err
			}
			if len(stats.Requests) == 0 {
				return false, errors.New("knowledge.judge: 取消前没有派发判断请求")
			}
			return !slices.ContainsFunc(stats.Requests, func(row struct {
				Path      string `json:"path"`
				State     string `json:"state"`
				Candidate string `json:"candidate"`
			}) bool {
				return row.State != "cancelled"
			}), nil
		}); err != nil {
			return err
		}
	}
	if err := d.request(http.MethodPut, path, owner.token, knowledgeRerankUpdate(knowledgeRerankConfig(true, "judge", judgeID, 10)), nil); err != nil {
		return err
	}
	return nil
}
