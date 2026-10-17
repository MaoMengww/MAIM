package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
)

type readingDocumentPage struct {
	Items []struct {
		ID     entityID `json:"doc_id"`
		KBID   entityID `json:"kb_id"`
		Title  string   `json:"title"`
		Status string   `json:"status"`
	} `json:"items"`
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	Total      int  `json:"total"`
	NextOffset int  `json:"next_offset"`
	HasMore    bool `json:"has_more"`
}

type readingChunk struct {
	KBID     entityID        `json:"kb_id"`
	DocID    entityID        `json:"doc_id"`
	ChunkID  entityID        `json:"chunk_id"`
	Index    int             `json:"chunk_index"`
	Content  string          `json:"content"`
	Metadata json.RawMessage `json:"metadata"`
}

type readingDocumentRead struct {
	Items       []readingChunk `json:"items"`
	Total       int            `json:"total"`
	NextOffset  int            `json:"next_offset"`
	HasMore     bool           `json:"has_more"`
	Truncated   bool           `json:"truncated"`
	ActualStart *int           `json:"actual_start_index"`
	ActualEnd   *int           `json:"actual_end_index"`
	Exhaustive  bool           `json:"exhaustive"`
}

type readingHistoryItem struct {
	MessageID entityID       `json:"message_id"`
	Seq       sequenceNumber `json:"seq"`
	Content   string         `json:"content"`
}

type readingHistoryRead struct {
	Items      []readingHistoryItem `json:"items"`
	NextCursor sequenceNumber       `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
	Truncated  bool                 `json:"truncated"`
}

type readingPublicChunk struct {
	ID       entityID `json:"id"`
	DocID    entityID `json:"doc_id"`
	Index    int      `json:"chunk_index"`
	Content  string   `json:"content"`
	Metadata string   `json:"metadata"`
}

type readingAnswer struct {
	DocumentPages []readingDocumentPage `json:"document_pages"`
	DocumentReads []readingDocumentRead `json:"document_reads"`
	HistoryReads  []readingHistoryRead  `json:"history_reads"`
	Errors        []struct {
		Category string `json:"category"`
	} `json:"errors"`
	Gap     bool `json:"gap"`
	Bounded bool `json:"bounded"`
}

func (f *knowledgeToolsFixture) readingRun(label string, calls []map[string]any) (publicRun, readingAnswer, error) {
	run, _, err := f.searchRun("READING_"+label, calls)
	var answer readingAnswer
	if err == nil {
		err = json.Unmarshal([]byte(run.ResultText), &answer)
	}
	return run, answer, err
}

func (f *knowledgeToolsFixture) documentCatalog() (result error) {
	prefix := "reading-catalog-" + f.suffix
	var docs []documentView
	for _, title := range []string{prefix + "-alpha", prefix + "-beta", "irrelevant-" + f.suffix} {
		doc, err := f.d.uploadKnowledge(f.requester, f.kb, "catalog.txt", title, "Catalog identity proof "+title)
		if err != nil {
			return err
		}
		docs = append(docs, doc)
		defer func() {
			result = errors.Join(result, f.d.request(http.MethodDelete, "/knowledge/documents/"+doc.ID.String(), f.requester.token, nil, nil))
		}()
		if err = f.d.awaitKnowledgeDocument(f.requester, doc, "ready", time.Now().Add(f.d.ingestTimeout)); err != nil {
			return err
		}
	}
	var public struct {
		Items []documentView `json:"items"`
		Total int64          `json:"total,string"`
	}
	path := "/knowledge/bases/" + f.kb.String() + "/documents?title=" + prefix + "&offset=0&limit=1"
	if err := f.d.request(http.MethodGet, path, f.requester.token, nil, &public); err != nil {
		return err
	}
	if len(public.Items) != 1 || public.Total != 2 || !slices.Contains([]entityID{docs[0].ID, docs[1].ID}, public.Items[0].ID) {
		return errors.New("document catalog: public title filter/pagination did not select exactly the two matching real documents")
	}
	_, answer, err := f.readingRun("CATALOG", []map[string]any{
		{"tool": "list_knowledge_documents", "kb_id": f.kb, "title": prefix, "offset": 0, "limit": 1},
		{"tool": "list_knowledge_documents", "kb_id": f.kb, "title": prefix, "continue_catalog": true, "limit": 1},
	})
	if err != nil {
		return err
	}
	if len(answer.DocumentPages) != 2 || len(answer.Errors) != 0 {
		return fmt.Errorf("document catalog: Bot did not deliver two real filtered pages: %+v", answer)
	}
	seen := map[entityID]bool{}
	for index, page := range answer.DocumentPages {
		if len(page.Items) != 1 || page.Total != 2 || page.HasMore != (index == 0) || page.NextOffset != index+1 {
			return errors.New("document catalog: bounded page or continuation is not truthful")
		}
		item := page.Items[0]
		if item.KBID != f.kb || item.Status != "ready" || !slices.ContainsFunc(docs[:2], func(doc documentView) bool { return item.ID == doc.ID && item.Title == doc.Title }) || seen[item.ID] {
			return errors.New("document catalog: invented, duplicate or unrelated document title entered answer")
		}
		seen[item.ID] = true
	}
	return nil
}

func (f *knowledgeToolsFixture) documentReading() (result error) {
	var text strings.Builder
	for index := range 6 {
		fmt.Fprintf(&text, "Reading restriction %d nonce=%s. %s\n\n", index, f.suffix, strings.Repeat(fmt.Sprintf("condition-%d; ", index), 90))
	}
	doc, err := f.d.uploadKnowledge(f.requester, f.kb, "reading.txt", "reading-adjacent-"+f.suffix, text.String())
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodDelete, "/knowledge/documents/"+doc.ID.String(), f.requester.token, nil, nil))
	}()
	if err = f.d.awaitKnowledgeDocument(f.requester, doc, "ready", time.Now().Add(f.d.ingestTimeout)); err != nil {
		return err
	}
	var public struct {
		Chunks []readingPublicChunk `json:"chunks"`
	}
	if err = f.d.request(http.MethodGet, "/knowledge/documents/"+doc.ID.String()+"/chunks?limit=100", f.requester.token, nil, &public); err != nil {
		return err
	}
	if len(public.Chunks) < 4 {
		return errors.New("reading fixture did not produce enough real neighboring chunks")
	}
	start, end := public.Chunks[1].Index, public.Chunks[3].Index
	run, answer, err := f.readingRun("RANGE", []map[string]any{
		{"tool": "read_knowledge_document", "doc_id": doc.ID, "start_index": start, "end_index": end, "limit": 1},
		{"tool": "read_knowledge_document", "doc_id": doc.ID, "start_index": start, "end_index": end, "continue_document": true, "limit": 2},
		{"tool": "read_knowledge_document", "doc_id": doc.ID, "start_index": end + 1, "end_index": end, "limit": 1},
	})
	if err != nil {
		return err
	}
	if len(answer.DocumentReads) != 2 || len(answer.Errors) != 1 || answer.Errors[0].Category != "invalid_argument" {
		return errors.New("range reading did not distinguish real pages from reversed-range failure")
	}
	first, second := answer.DocumentReads[0], answer.DocumentReads[1]
	if len(first.Items) != 1 || !first.HasMore || !first.Truncated || first.NextOffset != 1 || first.Total != 3 || len(second.Items) != 2 || second.HasMore || second.Total != 3 {
		return errors.New("range reading did not preserve range-relative pagination and truthful continuation")
	}
	for pageIndex, page := range answer.DocumentReads {
		if page.Exhaustive || page.ActualStart == nil || page.ActualEnd == nil || *page.ActualStart != page.Items[0].Index || *page.ActualEnd != page.Items[len(page.Items)-1].Index {
			return errors.New("local reading claimed a whole document or fabricated actual range")
		}
		for index, item := range page.Items {
			want := public.Chunks[1+pageIndex+index]
			if err = f.readingChunkEvidence(run, item, doc, want); err != nil {
				return err
			}
		}
	}
	if err = f.d.runtimeReject(http.MethodGet, "/knowledge/documents/"+doc.ID.String()+"/chunks", f.member, nil, http.StatusForbidden); err != nil {
		return err
	}
	_, denied, err := f.readingRun("FORGED_DOCUMENT_IDENTITY", []map[string]any{{"tool": "read_knowledge_document", "doc_id": doc.ID, "requester_id": f.owner.id, "conv_id": f.conv}})
	if err != nil {
		return err
	}
	if len(denied.DocumentReads) != 0 || len(denied.Errors) != 1 || denied.Errors[0].Category != "invalid_argument" {
		return errors.New("document read accepted model-controlled authority")
	}
	return nil
}

func (f *knowledgeToolsFixture) readingChunkEvidence(run publicRun, item readingChunk, doc documentView, want readingPublicChunk) error {
	if item.KBID != f.kb || item.DocID != doc.ID || item.ChunkID != want.ID || item.Index != want.Index || item.Content != want.Content || item.Content == "" {
		return errors.New("document answer did not use the actual parsed chunk identity/content")
	}
	if !slices.ContainsFunc(run.Sources, func(source runtimeKnowledgeSource) bool {
		return source.KBID == f.kb && source.DocID == doc.ID && source.ChunkID == want.ID
	}) {
		return errors.New("document answer lost its real persisted chunk citation")
	}
	if len(item.Metadata) > 0 {
		var actual, original map[string]any
		if err := json.Unmarshal(item.Metadata, &actual); err != nil {
			return err
		}
		if want.Metadata != "" {
			if err := json.Unmarshal([]byte(want.Metadata), &original); err != nil {
				return err
			}
		}
		for key, value := range actual {
			if !reflect.DeepEqual(value, original[key]) {
				return errors.New("document reading invented location metadata absent from real parsed chunk")
			}
		}
	}
	return nil
}
