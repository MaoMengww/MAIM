package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
)

type knowledgeContractItem struct {
	KBID           entityID `json:"kb_id"`
	DocID          entityID `json:"doc_id"`
	ChunkID        entityID `json:"chunk_id"`
	Content        string   `json:"content"`
	MatchedContent string   `json:"matched_content"`
	KBName         string   `json:"kb_name"`
	Title          string   `json:"doc_title"`
	MetadataJSON   string   `json:"metadata_json"`
}

type knowledgeContractDiscovery struct {
	Items []struct {
		KBID        entityID `json:"kb_id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Mode        string   `json:"mode"`
	} `json:"items"`
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	Total   int  `json:"total"`
	HasMore bool `json:"has_more"`
}

type knowledgeContractAnswer struct {
	Evidence    []knowledgeContractItem      `json:"evidence"`
	Discoveries []knowledgeContractDiscovery `json:"discoveries"`
	Errors      []struct {
		Category string `json:"category"`
	} `json:"errors"`
}

func (f *knowledgeToolsFixture) contractRun(label string, grants []entityID, calls []map[string]any) (publicRun, knowledgeContractAnswer, error) {
	token := "KNOWLEDGE_CONTRACT_" + label + "_" + f.suffix
	if err := f.plan(token, calls, -1, false); err != nil {
		return publicRun{}, knowledgeContractAnswer{}, err
	}
	run, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token, map[string]any{"resource_grant_ids": grants})
	if err != nil {
		return publicRun{}, knowledgeContractAnswer{}, err
	}
	finished, err := f.d.waitRun(f.requester, run.ID)
	if err != nil {
		return finished, knowledgeContractAnswer{}, err
	}
	var answer knowledgeContractAnswer
	if finished.Status != "succeeded" || finished.OutputStatus != "committed" {
		return finished, answer, fmt.Errorf("contract %s: status=%s error=%s", label, finished.Status, finished.Error)
	}
	if err := json.Unmarshal([]byte(finished.ResultText), &answer); err != nil {
		return finished, answer, err
	}
	return finished, answer, f.uniqueMessage(finished)
}

// Runs only through authenticated gateway calls, real ingestion/Milvus and the
// external reasoning/embedding fixture. No knowledge or authorization mocks.
func (f *knowledgeToolsFixture) searchContract() (result error) {
	d := f.d
	var original []resourceAuthorization
	if err := d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "list", "bot_id": f.bot.ID, "conv_id": f.conv}, &original); err != nil {
		return err
	}
	var grants []entityID
	for _, grant := range original {
		if grant.ResourceID == f.kb && grant.RevokedAt == nil && grant.CanRead && grant.CanShare {
			grants = append(grants, grant.ID)
		}
	}
	if len(grants) == 0 {
		return errors.New("contract fixture missing initial knowledge grant")
	}
	const purpose = "Deployment constraints and verification evidence"
	create := func(owner account, name string, model entityID) (entityID, error) {
		var kb struct {
			ID entityID `json:"id"`
		}
		err := d.request(http.MethodPost, "/knowledge/bases", owner.token, map[string]any{
			"name": name, "description": purpose, "mode": "rag", "embedding_model_id": model,
			"pipeline_config": map[string]any{"parsing": map[string]any{"engines": []string{"builtin"}}, "chunking": map[string]any{"chunk_size": 1024}, "retrieval": map[string]any{"mode": "vector", "score_threshold": 0.7}},
		}, &kb)
		return kb.ID, err
	}
	second, err := create(f.requester, "contract-compatible-"+f.suffix, f.embedding)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/knowledge/bases/"+second.String(), f.requester.token, nil, nil))
	}()
	private, err := create(f.requester, "contract-ungranted-"+f.suffix, f.embedding)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/knowledge/bases/"+private.String(), f.requester.token, nil, nil))
	}()
	foreign, err := create(f.owner, "contract-foreign-"+f.suffix, f.embedding)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/knowledge/bases/"+foreign.String(), f.owner.token, nil, nil))
	}()
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/knowledge", f.requester.token, map[string]any{"kb_id": second}, nil); err != nil {
		return err
	}
	grant, err := d.resourceGrant(f.requester, f.bot.ID, f.conv, "knowledge", second, "conversation", true)
	if err != nil {
		return err
	}
	grants = append(grants, grant.ID)
	defer func() {
		result = errors.Join(result, d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil))
	}()
	contents := map[entityID]string{f.doc.ID: f.content}
	var secondDocs []entityID
	for i := range 6 {
		content := fmt.Sprintf("P5_KNOWN_ANCHOR contract deployment rule %d nonce=%s", i, uuid.NewString())
		doc, err := d.uploadKnowledge(f.requester, second, "contract.txt", fmt.Sprintf("contract-%d-%s", i, f.suffix), content)
		if err != nil {
			return err
		}
		if err = d.awaitKnowledgeDocument(f.requester, doc, "ready", time.Now().Add(d.ingestTimeout)); err != nil {
			return err
		}
		contents[doc.ID] = content
		secondDocs = append(secondDocs, doc.ID)
	}
	type response struct {
		Items      []knowledgeContractItem `json:"items"`
		Limit      int                     `json:"limit"`
		Exhaustive bool                    `json:"exhaustive"`
	}
	query := "P5_KNOWN_ANCHOR deployment"
	public := func(body map[string]any) (response, error) {
		var rsp response
		err := d.request(http.MethodPost, "/knowledge/search", f.requester.token, body, &rsp)
		return rsp, err
	}
	checkItems := func(items []knowledgeContractItem, allowed []entityID, count int) error {
		if len(items) != count {
			return fmt.Errorf("expected %d real index results, got %d", count, len(items))
		}
		seen := map[entityID]bool{}
		for _, item := range items {
			if !slices.Contains(allowed, item.DocID) || seen[item.ChunkID] || item.ChunkID == "" || item.Content != contents[item.DocID] || item.MatchedContent != contents[item.DocID] {
				return fmt.Errorf("invalid, duplicate or out-of-filter source: %+v", item)
			}
			seen[item.ChunkID] = true
		}
		return nil
	}
	allDocs := append([]entityID{f.doc.ID}, secondDocs...)
	if err = d.broadcastPoll("contract compatible libraries indexed", func(bounded *driver) (bool, error) {
		var rsp response
		if err := bounded.request(http.MethodPost, "/knowledge/search", f.requester.token, map[string]any{"query": query, "conv_id": f.conv, "limit": 50}, &rsp); err != nil {
			return false, err
		}
		return len(rsp.Items) == 7, nil
	}); err != nil {
		return err
	}
	for _, ids := range [][]entityID{{f.kb, second, f.kb}, {second, f.kb, second}} {
		rsp, err := public(map[string]any{"query": query, "conv_id": f.conv, "kb_ids": ids, "limit": 50})
		if err != nil {
			return err
		}
		if err = checkItems(rsp.Items, allDocs, 7); err != nil {
			return err
		}
		if rsp.Limit != 50 || rsp.Exhaustive {
			return errors.New("public search omitted bounded/non-exhaustive semantics")
		}
		for _, item := range rsp.Items {
			if item.KBName == "" || item.Title == "" || item.MetadataJSON == "" {
				return errors.New("public search missing actual source metadata")
			}
		}
	}
	for _, test := range []struct {
		label        string
		body         map[string]any
		docs         []entityID
		count, limit int
	}{
		{"default", map[string]any{"query": query, "conv_id": f.conv}, allDocs, 5, 5},
		{"kb", map[string]any{"query": query, "conv_id": f.conv, "kb_ids": []entityID{f.kb}}, []entityID{f.doc.ID}, 1, 5},
		{"doc", map[string]any{"query": query, "conv_id": f.conv, "doc_ids": []entityID{secondDocs[0]}}, secondDocs[:1], 1, 5},
		{"limit", map[string]any{"query": query, "conv_id": f.conv, "limit": 2}, allDocs, 2, 2},
		{"empty", map[string]any{"query": query}, nil, 0, 5},
	} {
		rsp, err := public(test.body)
		if err != nil {
			return fmt.Errorf("public %s: %w", test.label, err)
		}
		if err = checkItems(rsp.Items, test.docs, test.count); err != nil {
			return fmt.Errorf("public %s: %w", test.label, err)
		}
		if rsp.Limit != test.limit {
			return fmt.Errorf("public %s: unexpected result bound", test.label)
		}
	}
	for _, body := range []map[string]any{
		{"query": query, "kb_ids": []entityID{f.kb, foreign}},
		{"query": query, "conv_id": f.conv, "kb_ids": []entityID{private}},
		{"query": query, "conv_id": f.conv, "kb_ids": []entityID{f.kb}, "doc_ids": []entityID{secondDocs[0]}},
	} {
		if err = d.runtimeReject(http.MethodPost, "/knowledge/search", f.requester, body, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err = d.runtimeReject(http.MethodPost, "/knowledge/search", f.owner, map[string]any{"query": query, "doc_ids": []entityID{f.doc.ID}}, http.StatusForbidden); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/knowledge/bases/"+f.kb.String()+"/search", f.requester, map[string]any{"query": query, "kb_ids": []entityID{second}}, http.StatusBadRequest); err != nil {
		return err
	}
	for _, limit := range []int{0, -1, 51} {
		if err = d.runtimeReject(http.MethodPost, "/knowledge/search", f.requester, map[string]any{"query": query, "kb_ids": []entityID{f.kb}, "limit": limit}, http.StatusBadRequest); err != nil {
			return err
		}
	}
	for _, test := range []struct {
		label    string
		selected []entityID
		args     map[string]any
		docs     []entityID
		count    int
	}{
		{"DEFAULT", grants, map[string]any{"query": query, "limit": 50}, allDocs, 7},
		{"REVERSED", []entityID{grants[1], grants[0]}, map[string]any{"query": query, "limit": 50}, allDocs, 7},
		{"KB", grants, map[string]any{"query": query, "kb_ids": []entityID{f.kb}}, []entityID{f.doc.ID}, 1},
		{"DOC", grants, map[string]any{"query": query, "doc_ids": []entityID{secondDocs[0]}}, secondDocs[:1], 1},
		{"LIMIT", grants, map[string]any{"query": query, "limit": 2}, allDocs, 2},
		{"NONE", []entityID{}, map[string]any{"query": query}, nil, 0},
	} {
		if test.label == "DEFAULT" {
			test.args["query"] = query + " KNOWLEDGE_CONTRACT_DEFAULT_" + f.suffix
		}
		run, answer, err := f.contractRun(test.label, test.selected, []map[string]any{test.args})
		if err != nil {
			return err
		}
		if len(answer.Errors) != 0 {
			return fmt.Errorf("Bot %s returned tool error", test.label)
		}
		if err = checkItems(answer.Evidence, test.docs, test.count); err != nil {
			return fmt.Errorf("Bot %s: %w", test.label, err)
		}
		for _, item := range answer.Evidence {
			if !slices.ContainsFunc(run.Sources, func(source runtimeKnowledgeSource) bool {
				return source.DocID == item.DocID && source.ChunkID == item.ChunkID
			}) {
				return errors.New("Bot lost actual source citation")
			}
		}
		if test.label == "DEFAULT" {
			observed, err := f.observeKnowledge("KNOWLEDGE_CONTRACT_DEFAULT_" + f.suffix)
			if err != nil {
				return err
			}
			if len(observed.Embeddings) != 1 {
				return fmt.Errorf("compatible multi-library search issued %d query embeddings, want 1", len(observed.Embeddings))
			}
		}
	}
	_, discovery, err := f.contractRun("DISCOVERY", grants, []map[string]any{{"tool": "list_knowledge_bases", "limit": 1}, {"tool": "list_knowledge_bases", "offset": 1, "limit": 1}})
	if err != nil {
		return err
	}
	if len(discovery.Discoveries) != 2 {
		return errors.New("Bot library discovery did not return both pages")
	}
	var listed []entityID
	for index, page := range discovery.Discoveries {
		if page.Total != 2 || page.Offset != index || page.Limit != 1 || page.HasMore != (index == 0) || len(page.Items) != 1 {
			return errors.New("library discovery pagination incorrect")
		}
		item := page.Items[0]
		if item.Name == "" || item.Mode != "rag" || !slices.Contains([]entityID{f.kb, second}, item.KBID) || slices.Contains(listed, item.KBID) {
			return errors.New("library discovery leaked/duplicated resource")
		}
		if item.KBID == second && item.Description != purpose {
			return errors.New("library purpose missing")
		}
		listed = append(listed, item.KBID)
	}
	_, empty, err := f.contractRun("DISCOVERY_EMPTY", []entityID{}, []map[string]any{{"tool": "list_knowledge_bases"}})
	if err != nil {
		return err
	}
	if len(empty.Discoveries) != 1 || empty.Discoveries[0].Total != 0 || len(empty.Discoveries[0].Items) != 0 {
		return errors.New("empty Run authority fell back to bound or owned libraries")
	}
	for _, args := range []map[string]any{{"query": query, "limit": 0}, {"query": query, "limit": 51}, {"query": query, "candidate_top_k": 50}, {"tool": "list_knowledge_bases", "limit": 101}, {"tool": "list_knowledge_bases", "offset": -1}} {
		_, answer, err := f.contractRun("INVALID_"+uuid.NewString(), grants, []map[string]any{args})
		if err != nil {
			return err
		}
		if len(answer.Errors) != 1 || answer.Errors[0].Category != "invalid_argument" {
			return errors.New("invalid business/tool limits were not rejected consistently")
		}
	}
	for _, args := range []map[string]any{{"query": query, "kb_ids": []entityID{private}}, {"query": query, "kb_ids": []entityID{f.kb}, "doc_ids": []entityID{secondDocs[0]}}} {
		run, _, err := f.contractRun("DENIED_"+uuid.NewString(), grants, []map[string]any{args})
		if err == nil {
			return errors.New("explicit unauthorized Bot filter was not denied")
		}
		var denied *apiError
		if errors.As(err, &denied) {
			if denied.status != http.StatusForbidden {
				return err
			}
		} else if run.Status != "failed" || run.OutputStatus == "committed" || run.MessageID != nil {
			return err
		}
	}
	// Both references yield the same fixture dimension, but different registered
	// model identities must never be treated as compatible vector spaces.
	otherModel, err := d.fixtureModel(f.requester, "embed", "fixture-embed-knowledge-tools-other-"+f.suffix)
	if err != nil {
		return err
	}
	for _, config := range []map[string]any{
		{"embedding_model_id": otherModel},
		{"embedding_model_id": f.embedding, "pipeline_config": map[string]any{"retrieval": map[string]any{"mode": "vector", "score_threshold": 0}}},
	} {
		if err = d.request(http.MethodPut, "/knowledge/bases/"+second.String(), f.requester.token, config, nil); err != nil {
			return err
		}
		for _, ids := range [][]entityID{{f.kb, second}, {second, f.kb}} {
			err = d.request(http.MethodPost, "/knowledge/search", f.requester.token, map[string]any{"query": query, "kb_ids": ids}, nil)
			var rejected *apiError
			if !errors.As(err, &rejected) || rejected.status != http.StatusConflict || rejected.code != 1019 {
				return fmt.Errorf("incompatible public scope must be HTTP409/code1019, got %v", err)
			}
		}
		for _, selected := range [][]entityID{grants, {grants[1], grants[0]}} {
			_, answer, err := f.contractRun("INCOMPATIBLE_"+uuid.NewString(), selected, []map[string]any{{"query": query}, {"query": query, "kb_ids": []entityID{f.kb}}})
			if err != nil {
				return err
			}
			if len(answer.Errors) != 1 || answer.Errors[0].Category != "incompatible_scope" {
				return errors.New("Bot incompatible scope was hidden as empty/configuration")
			}
			if err = checkItems(answer.Evidence, []entityID{f.doc.ID}, 1); err != nil {
				return err
			}
		}
	}
	if err = d.request(http.MethodPut, "/knowledge/bases/"+second.String(), f.requester.token, map[string]any{"embedding_model_id": f.embedding, "pipeline_config": map[string]any{"retrieval": map[string]any{"mode": "vector", "score_threshold": 0.7}}}, nil); err != nil {
		return err
	}
	restored, err := public(map[string]any{"query": query, "conv_id": f.conv, "limit": 50})
	if err != nil {
		return err
	}
	if err = checkItems(restored.Items, allDocs, 7); err != nil {
		return fmt.Errorf("public restored compatible scope: %w", err)
	}
	_, restoredAnswer, err := f.contractRun("RESTORED", grants, []map[string]any{{"query": query, "limit": 50}})
	if err != nil {
		return err
	}
	if len(restoredAnswer.Errors) != 0 {
		return errors.New("Bot restored compatible scope returned tool error")
	}
	if err = checkItems(restoredAnswer.Evidence, allDocs, 7); err != nil {
		return fmt.Errorf("Bot restored compatible scope: %w", err)
	}
	// A completed listing is not permanent permission to consume or publish its
	// metadata. Revoke after the real read, before another action can execute.
	token := "KNOWLEDGE_CONTRACT_CATALOG_REVOKE_" + f.suffix
	if err = f.plan(token, []map[string]any{{"tool": "list_knowledge_bases"}, {"query": query + " " + token}}, 1, false); err != nil {
		return err
	}
	started, err := d.interactionStart(f.requester, f.bot.ID, f.conv, token, map[string]any{"resource_grant_ids": grants})
	if err != nil {
		return err
	}
	if err = f.heldKnowledge(token); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}
	if err = f.releaseKnowledge(token); err != nil {
		return err
	}
	finished, err := d.waitRun(f.requester, started.ID)
	var denied *apiError
	if err != nil && (!errors.As(err, &denied) || denied.status != http.StatusForbidden) {
		return err
	}
	if finished.MessageID != nil || finished.OutputStatus == "committed" {
		return errors.New("revoked catalog metadata was published")
	}
	observed, err := f.observeKnowledge(token)
	if err != nil {
		return err
	}
	if len(observed.Embeddings) != 0 {
		return errors.New("catalog revocation allowed another search")
	}
	fmt.Println("E2E evidence: issue02 public gateway and Bot multi-library/default/filter/limit/discovery/denial/order-independent incompatibility PASS")
	return nil
}
