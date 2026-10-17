package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (f *knowledgeToolsFixture) documentFormats() (result error) {
	fact := "HTML deployment remains single-writer; nonce=" + f.suffix
	hidden := "SCRIPT_NOT_READABLE_" + f.suffix
	raw := "<!DOCTYPE html><html><head><script>" + hidden + "</script></head><body><h1>Deployment constraints</h1><p>" + fact + "</p></body></html>"
	doc, err := f.d.uploadKnowledge(f.requester, f.kb, "reading.html", "reading-html-"+f.suffix, raw)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodDelete, "/knowledge/documents/"+doc.ID.String(), f.requester.token, nil, nil))
	}()
	if err = f.d.awaitKnowledgeDocument(f.requester, doc, "ready", time.Now().Add(f.d.ingestTimeout)); err != nil {
		return err
	}
	var chunks struct {
		Chunks []readingPublicChunk `json:"chunks"`
	}
	if err = f.d.request(http.MethodGet, "/knowledge/documents/"+doc.ID.String()+"/chunks?limit=100", f.requester.token, nil, &chunks); err != nil {
		return err
	}
	run, answer, err := f.readingRun("HTML", []map[string]any{{"tool": "read_knowledge_document", "doc_id": doc.ID, "limit": 10}})
	if err != nil {
		return err
	}
	if len(answer.DocumentReads) != 1 || len(answer.DocumentReads[0].Items) != len(chunks.Chunks) || len(chunks.Chunks) == 0 {
		return errors.New("HTML document did not deliver its real parser output")
	}
	var parsed strings.Builder
	for index, item := range answer.DocumentReads[0].Items {
		if err = f.readingChunkEvidence(run, item, doc, chunks.Chunks[index]); err != nil {
			return err
		}
		parsed.WriteString(item.Content)
	}
	if !strings.Contains(parsed.String(), fact) || strings.Contains(parsed.String(), "<html>") || strings.Contains(parsed.String(), hidden) {
		return errors.New("model received raw HTML/script bytes instead of readable parsed content")
	}
	var original struct {
		Content string `json:"content"`
	}
	if err = f.d.request(http.MethodGet, "/knowledge/documents/"+doc.ID.String()+"/content", f.requester.token, nil, &original); err != nil {
		return err
	}
	if original.Content != raw {
		return errors.New("original file API no longer preserves its separate raw-content semantics")
	}
	var large strings.Builder
	for index := range 110 {
		fmt.Fprintf(&large, "Bounded reading paragraph %03d nonce=%s. %s\n\n", index, f.suffix, strings.Repeat("single-writer restriction; ", 40))
	}
	big, err := f.d.uploadKnowledge(f.requester, f.kb, "bounded-reading.txt", "reading-byte-bound-"+f.suffix, large.String())
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodDelete, "/knowledge/documents/"+big.ID.String(), f.requester.token, nil, nil))
	}()
	if err = f.d.awaitKnowledgeDocument(f.requester, big, "ready", time.Now().Add(f.d.ingestTimeout)); err != nil {
		return err
	}
	var public json.RawMessage
	if err = f.d.request(http.MethodGet, "/knowledge/documents/"+big.ID.String()+"/chunks?limit=100", f.requester.token, nil, &public); err != nil {
		return err
	}
	var first struct {
		Chunks     []readingPublicChunk `json:"chunks"`
		Total      int64                `json:"total,string"`
		NextOffset int                  `json:"next_offset"`
		HasMore    bool                 `json:"has_more"`
		Truncated  bool                 `json:"truncated"`
		Start      *int                 `json:"actual_start_index"`
		End        *int                 `json:"actual_end_index"`
	}
	if err = json.Unmarshal(public, &first); err != nil {
		return err
	}
	if len(public) > 65536 || len(first.Chunks) == 0 || int64(len(first.Chunks)) >= first.Total || !first.HasMore || !first.Truncated || first.NextOffset != len(first.Chunks) || first.Start == nil || first.End == nil {
		return errors.New("parsed chunk output did not enforce its public 64 KiB bound with resumable real coverage")
	}
	var next struct {
		Chunks []readingPublicChunk `json:"chunks"`
	}
	path := fmt.Sprintf("/knowledge/documents/%s/chunks?offset=%d&limit=1", big.ID, first.NextOffset)
	if err = f.d.request(http.MethodGet, path, f.requester.token, nil, &next); err != nil {
		return err
	}
	if len(next.Chunks) != 1 || next.Chunks[0].Index <= *first.End {
		return errors.New("byte-truncated page cannot resume at the next real parsed chunk")
	}
	_, limited, err := f.readingRun("BOUNDED", []map[string]any{{"tool": "read_knowledge_document", "doc_id": big.ID, "limit": 1}})
	if err != nil {
		return err
	}
	if len(limited.DocumentReads) != 1 || len(limited.DocumentReads[0].Items) != 1 || !limited.DocumentReads[0].HasMore || !limited.DocumentReads[0].Truncated || limited.DocumentReads[0].Exhaustive {
		return errors.New("Bot limited reading hid truncation or claimed a complete document")
	}
	failed, err := f.d.uploadKnowledge(f.requester, f.kb, "failed-reading.txt", "reading-unavailable-"+f.suffix, "P5_FAIL_INGEST real external embedding rejection "+f.suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodDelete, "/knowledge/documents/"+failed.ID.String(), f.requester.token, nil, nil))
	}()
	if err = f.d.awaitKnowledgeDocument(f.requester, failed, "failed", time.Now().Add(f.d.ingestTimeout)); err != nil {
		return err
	}
	if err = f.d.runtimeReject(http.MethodGet, "/knowledge/documents/"+failed.ID.String()+"/chunks", f.requester, nil, http.StatusServiceUnavailable); err != nil {
		return err
	}
	_, unavailable, err := f.readingRun("FAILED_DOCUMENT", []map[string]any{{"tool": "read_knowledge_document", "doc_id": failed.ID, "limit": 1}})
	if err != nil {
		return err
	}
	if len(unavailable.DocumentReads) != 0 || len(unavailable.Errors) != 1 || unavailable.Errors[0].Category != "document_unavailable" || !unavailable.Gap {
		return errors.New("unavailable parsed document was fabricated as content or confused with an empty search result")
	}
	return nil
}
