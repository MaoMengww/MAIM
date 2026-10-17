package run

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	kb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type documentListResult struct {
	Status     string                                    `json:"status"`
	Items      []resourcegrant.KnowledgeDocumentMetadata `json:"items"`
	Offset     int32                                     `json:"offset"`
	Limit      int32                                     `json:"limit"`
	Total      int64                                     `json:"total"`
	NextOffset int32                                     `json:"next_offset"`
	HasMore    bool                                      `json:"has_more"`
	Truncated  bool                                      `json:"truncated"`
}

func (s *Service) executeDocumentList(ctx context.Context, r *model.Run, arguments string) (*knowledgeSearchOutcome, error) {
	args, err := decodeDocumentList(arguments)
	if err != nil {
		return knowledgeFailure("invalid_argument"), nil
	}
	if s.kb == nil {
		return knowledgeFailure("unavailable"), nil
	}
	result, err := s.kb.ListDocuments(resourcegrant.Context(ctx, resourceAccess(r)), &kb.ListDocumentsReq{KbId: args.KBID, Title: args.Title, Offset: args.Offset, Limit: args.Limit})
	if err != nil {
		return knowledgeReadError(ctx, err)
	}
	if _, err := resourcegrant.Check(s.svc.DB.WithContext(ctx), resourceAccess(r), "knowledge", args.KBID, r.Visibility != "personal", false); err != nil {
		return nil, err
	}
	if result == nil || len(result.Items) > int(args.Limit) || result.Total < 0 {
		return knowledgeFailure("unavailable"), nil
	}
	response := documentListResult{Status: "ok", Items: []resourcegrant.KnowledgeDocumentMetadata{}, Offset: args.Offset, Limit: args.Limit, Total: result.Total}
	outcome := &knowledgeSearchOutcome{}
	outputBytes := 512 // Reserve pagination and actual-range envelope fields.
	for _, item := range result.Items {
		if item == nil || item.KbId != args.KBID {
			return nil, status.Error(codes.PermissionDenied, "document outside authorized scope")
		}
		metadata, err := resourcegrant.CurrentKnowledgeDocumentMetadata(s.svc.DB.WithContext(ctx), item.Id, false)
		if err != nil {
			return knowledgeReadError(ctx, err)
		}
		if metadata.KBID != item.KbId || metadata.Title != item.Title || metadata.FileType != item.FileType || metadata.Status != item.Status || metadata.ChunkCount != int(item.ChunkCount) {
			return nil, status.Error(codes.Aborted, "document metadata changed during reading")
		}
		content, err := json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
		if outputBytes+len(content)+1 > knowledgeResultLimit {
			if len(response.Items) == 0 {
				return knowledgeFailure("reading_too_large"), nil
			}
			break
		}
		outputBytes += len(content) + 1
		response.Items = append(response.Items, *metadata)
		version := model.KnowledgeSourceVersion(metadata.SourceVersion())
		provenance, err := json.Marshal([]model.SourceVersion{version})
		if err != nil {
			return nil, err
		}
		outcome.Materials = append(outcome.Materials, Material{Kind: "knowledge_document_catalog", KnowledgeID: args.KBID, RequesterID: r.RequesterID, Shared: r.Visibility != "personal", Content: string(content), Provenance: provenance})
		outcome.Versions = append(outcome.Versions, version)
		outcome.Sources = append(outcome.Sources, map[string]any{"type": "knowledge_document", "kind": "catalog", "kb_id": args.KBID, "doc_id": metadata.ID, "title": metadata.Title})
	}
	response.NextOffset = args.Offset + int32(len(response.Items))
	response.HasMore = int64(response.NextOffset) < result.Total
	response.Truncated = response.HasMore
	raw, err := json.Marshal(response)
	outcome.Result = string(raw)
	return outcome, err
}

type documentReadItem struct {
	KBID       string          `json:"kb_id"`
	DocID      string          `json:"doc_id"`
	ChunkID    string          `json:"chunk_id"`
	ChunkIndex int             `json:"chunk_index"`
	Title      string          `json:"title"`
	Content    string          `json:"content"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type documentReadResult struct {
	Status           string             `json:"status"`
	Items            []documentReadItem `json:"items"`
	Offset           int32              `json:"offset"`
	Limit            int32              `json:"limit"`
	Total            int64              `json:"total"`
	ActualStartIndex *int               `json:"actual_start_index,omitempty"`
	ActualEndIndex   *int               `json:"actual_end_index,omitempty"`
	NextOffset       int32              `json:"next_offset"`
	HasMore          bool               `json:"has_more"`
	Truncated        bool               `json:"truncated"`
	Exhaustive       bool               `json:"exhaustive"`
}

func (s *Service) executeDocumentRead(ctx context.Context, r *model.Run, arguments string) (*knowledgeSearchOutcome, error) {
	args, err := decodeDocumentRead(arguments)
	if err != nil {
		return knowledgeFailure("invalid_argument"), nil
	}
	if s.kb == nil {
		return knowledgeFailure("unavailable"), nil
	}
	access := resourceAccess(r)
	document, err := s.kb.GetDocument(resourcegrant.Context(ctx, access), &kb.GetDocumentReq{DocId: args.DocID})
	if err != nil {
		return knowledgeReadError(ctx, err)
	}
	if document == nil || document.Id != args.DocID {
		return knowledgeFailure("unavailable"), nil
	}
	if _, err := resourcegrant.Check(s.svc.DB.WithContext(ctx), access, "knowledge", document.KbId, r.Visibility != "personal", false); err != nil {
		return nil, err
	}
	result, err := s.kb.ListChunks(resourcegrant.Context(ctx, access), &kb.ListChunksReq{DocId: args.DocID, Offset: args.Offset, Limit: args.Limit, StartIndex: args.StartIndex, EndIndex: args.EndIndex})
	if err != nil {
		return knowledgeReadError(ctx, err)
	}
	if result == nil || len(result.Chunks) > int(args.Limit) || result.Total < 0 {
		return knowledgeFailure("unavailable"), nil
	}
	response := documentReadResult{Status: "ok", Items: []documentReadItem{}, Offset: args.Offset, Limit: args.Limit, Total: result.Total}
	outcome := &knowledgeSearchOutcome{}
	outputBytes := 512
	last := -1
	for _, item := range result.Chunks {
		if item == nil || item.DocId != args.DocID || int(item.ChunkIndex) <= last || args.StartIndex != nil && item.ChunkIndex < *args.StartIndex || args.EndIndex != nil && item.ChunkIndex > *args.EndIndex {
			return knowledgeFailure("unavailable"), nil
		}
		chunk, err := resourcegrant.CurrentKnowledgeChunk(s.svc.DB.WithContext(ctx), item.Id, false)
		if err != nil {
			return nil, err
		}
		if chunk.DocID != args.DocID || chunk.KBID != document.KbId || chunk.ChunkIndex != int(item.ChunkIndex) || chunk.Content != item.Content {
			return nil, status.Error(codes.Aborted, "parsed document changed during reading")
		}
		read := documentReadItem{KBID: chunk.KBID, DocID: chunk.DocID, ChunkID: chunk.ID, ChunkIndex: chunk.ChunkIndex, Title: chunk.DocTitle, Content: chunk.Content, Metadata: chunk.Metadata}
		content, err := json.Marshal(read)
		if err != nil {
			return nil, err
		}
		if outputBytes+len(content)+1 > knowledgeResultLimit {
			if len(response.Items) == 0 {
				return knowledgeFailure("reading_too_large"), nil
			}
			break
		}
		outputBytes += len(content) + 1
		response.Items = append(response.Items, read)
		last = chunk.ChunkIndex
		version := model.KnowledgeSourceVersion(chunk.SourceVersion())
		provenance, err := json.Marshal([]model.SourceVersion{version})
		if err != nil {
			return nil, err
		}
		outcome.Materials = append(outcome.Materials, Material{Kind: "knowledge", KnowledgeID: chunk.KBID, RequesterID: r.RequesterID, Shared: r.Visibility != "personal", Content: string(content), Provenance: provenance})
		outcome.Versions = append(outcome.Versions, version)
		source := knowledgeSource(chunk, "document_read", "")
		source["role"] = "supplemental"
		delete(source, "matched_chunk_id")
		outcome.Sources = append(outcome.Sources, source)
	}
	if len(response.Items) > 0 {
		start, end := response.Items[0].ChunkIndex, response.Items[len(response.Items)-1].ChunkIndex
		response.ActualStartIndex, response.ActualEndIndex = &start, &end
	}
	response.NextOffset = args.Offset + int32(len(response.Items))
	response.HasMore = int64(response.NextOffset) < response.Total
	response.Truncated = response.HasMore
	raw, err := json.Marshal(response)
	outcome.Result = string(raw)
	return outcome, err
}
