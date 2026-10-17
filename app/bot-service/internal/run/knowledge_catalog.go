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

type knowledgeListResult struct {
	Status    string                            `json:"status"`
	Items     []resourcegrant.KnowledgeMetadata `json:"items"`
	Offset    int32                             `json:"offset"`
	Limit     int32                             `json:"limit"`
	Total     int64                             `json:"total"`
	HasMore   bool                              `json:"has_more"`
	Truncated bool                              `json:"truncated"`
}

func (s *Service) executeKnowledgeList(ctx context.Context, r *model.Run, arguments string) (*knowledgeSearchOutcome, error) {
	args, err := decodeKnowledgeList(arguments)
	if err != nil {
		return knowledgeFailure("invalid_argument"), nil
	}
	if s.kb == nil {
		return knowledgeFailure("unavailable"), nil
	}
	access := resourceAccess(r)
	result, err := s.kb.ListKBs(resourcegrant.Context(ctx, access), &kb.ListKBsReq{Offset: args.Offset, Limit: args.Limit})
	if err != nil {
		return knowledgeReadError(ctx, err)
	}
	if err := resourcegrant.CheckIDs(s.svc.DB.WithContext(ctx), access, r.Visibility != "personal", false); err != nil {
		return knowledgeReadError(ctx, err)
	}
	if result == nil || len(result.Items) > int(args.Limit) || result.Total < 0 {
		return knowledgeFailure("unavailable"), nil
	}
	outcome := &knowledgeSearchOutcome{}
	response := knowledgeListResult{Status: "ok", Items: []resourcegrant.KnowledgeMetadata{}, Offset: args.Offset, Limit: args.Limit, Total: result.Total}
	for _, item := range result.Items {
		if item == nil || item.Status != "active" || item.Mode != "rag" {
			return knowledgeFailure("unavailable"), nil
		}
		if _, err := resourcegrant.Check(s.svc.DB.WithContext(ctx), access, "knowledge", item.Id, r.Visibility != "personal", false); err != nil {
			return nil, err
		}
		metadata, err := resourcegrant.CurrentKnowledgeMetadata(s.svc.DB.WithContext(ctx), item.Id, false)
		if err != nil {
			return knowledgeReadError(ctx, err)
		}
		// Never substitute a different generation for metadata returned by the
		// service. A concurrent change crosses the same source control boundary.
		if metadata.Name != item.Name || metadata.Description != item.Description || metadata.Mode != item.Mode {
			return nil, status.Error(codes.Aborted, "knowledge metadata changed during reading")
		}
		response.Items = append(response.Items, *metadata)
		encoded, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		if len(encoded) > knowledgeResultLimit-32 {
			response.Items = response.Items[:len(response.Items)-1]
			if len(response.Items) == 0 {
				return knowledgeFailure("unavailable"), nil
			}
			response.Truncated = true
			break // Keep a contiguous page so offset + len(items) is the next cursor.
		}
		version := model.KnowledgeSourceVersion(metadata.SourceVersion())
		provenance, err := json.Marshal([]model.SourceVersion{version})
		if err != nil {
			return nil, err
		}
		content, err := json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
		outcome.Materials = append(outcome.Materials, Material{Kind: "knowledge_catalog", KnowledgeID: metadata.ID, RequesterID: r.RequesterID, Shared: r.Visibility != "personal", Content: string(content), Provenance: provenance})
		outcome.Sources = append(outcome.Sources, map[string]any{"type": "knowledge_base", "kind": "catalog", "kb_id": metadata.ID, "kb_name": metadata.Name})
		outcome.Versions = append(outcome.Versions, version)
	}
	response.HasMore = response.Truncated || int64(args.Offset)+int64(len(response.Items)) < result.Total
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	outcome.Result = string(raw)
	return outcome, nil
}
