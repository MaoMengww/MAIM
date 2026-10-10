package run

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/maomeng/aim/app/bot-service/internal/graph"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	kb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"slices"
)

func inputAccess(i Input) resourcegrant.Access {
	return resourcegrant.Access{BotID: i.BotID, ConvID: i.ConvID, BindingID: i.BindingID, RequesterID: i.RequesterID, Visibility: i.Visibility, GrantIDs: i.ResourceGrantIDs}
}
func resourceAccess(r *model.Run) resourcegrant.Access {
	return resourcegrant.Access{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: r.RequesterID, Visibility: r.Visibility, GrantIDs: r.ResourceGrantIDs}
}
func (s *Service) retrieveResources(ctx context.Context, input Input, query string) ([]graph.KbDocument, []string, error) {
	ids := []string{}
	access := inputAccess(input)
	for _, id := range input.ResourceGrantIDs {
		var g resourcegrant.Grant
		err := s.svc.DB.WithContext(ctx).Table(resourcegrant.Table("knowledge")).Where("id = ?", id).Take(&g).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if _, err := resourcegrant.Check(s.svc.DB.WithContext(ctx), access, "knowledge", g.ResourceID, input.Visibility != "personal", false); err != nil {
			return nil, nil, err
		}
		ids = append(ids, g.ResourceID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return []graph.KbDocument{}, ids, nil
	}
	if s.kb == nil {
		return nil, nil, status.Error(codes.Unavailable, "knowledge service unavailable")
	}
	result, err := s.kb.Retrieve(resourcegrant.Context(ctx, access), &kb.RetrieveReq{Query: query, BotId: &input.BotID, ConvId: &input.ConvID, KbIds: ids})
	if err != nil {
		return nil, nil, err
	}
	if err := resourcegrant.CheckIDs(s.svc.DB.WithContext(ctx), access, input.Visibility != "personal", false); err != nil {
		return nil, nil, err
	}
	docs := make([]graph.KbDocument, 0, len(result.Items))
	for _, d := range result.Items {
		docs = append(docs, graph.KbDocument{ChunkID: d.ChunkId, DocID: d.DocId, Title: d.DocTitle, Content: d.Content, MatchedContent: d.MatchedContent, Score: float64(d.Score), KbID: d.KbId, KbName: d.KbName})
	}
	return docs, ids, nil
}
func (s *Service) ResourceAuthorization(ctx context.Context, user, executorBot string, body []byte) ([]byte, error) {
	var req resourcegrant.Request
	if json.Unmarshal(body, &req) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid resource authorization")
	}
	if executorBot != "" && req.Op != "read" {
		return nil, status.Error(codes.PermissionDenied, "executor may only read authorized resources")
	}
	if req.Op == "read" {
		if identity.Validate(req.RunID) != nil || identity.Validate(req.GrantID) != nil {
			return nil, status.Error(codes.InvalidArgument, "run_id and grant_id required")
		}
		var r *model.Run
		var err error
		if executorBot != "" {
			r, err = s.repo.Get(ctx, req.RunID)
			if err == nil && (r.ExecutorType != "third_party" || !slices.Contains([]string{"accepted", "stop_rejected"}, r.ExecutorStatus) || req.Generation != r.ExecutionGeneration || req.InputVersion != r.InputVersion) {
				return nil, status.Error(codes.PermissionDenied, "current executor claim required")
			}
			if err == nil && (r.BotID != executorBot || r.Status == "stopping" || r.Status == "waiting_authorization" || r.Status == "succeeded" || r.Status == "failed" || r.Status == "stopped") {
				return nil, status.Error(codes.PermissionDenied, "executor resource access suspended")
			}
		} else {
			r, err = s.Get(ctx, req.RunID, user)
		}
		if err != nil {
			return nil, err
		}
		if !slices.Contains(r.ResourceGrantIDs, req.GrantID) {
			return nil, status.Error(codes.PermissionDenied, "grant was not selected for Run")
		}
		if err := s.authorize(ctx, r, false); err != nil {
			return nil, err
		}
		var g resourcegrant.Grant
		for _, kind := range []string{"file", "knowledge"} {
			err = s.svc.DB.WithContext(ctx).Table(resourcegrant.Table(kind)).Where("id = ?", req.GrantID).Take(&g).Error
			if err == nil {
				break
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		}
		if err != nil {
			return nil, status.Error(codes.PermissionDenied, "resource grant unavailable")
		}
		input := Input{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: r.RequesterID, Visibility: r.Visibility, ResourceGrantIDs: []string{req.GrantID}}
		checkCurrent := func() error {
			current, err := s.repo.Get(ctx, r.ID)
			if err != nil {
				return err
			}
			if executorBot != "" && (current.ExecutionGeneration != req.Generation || current.InputVersion != req.InputVersion || current.Status == "stopping" || current.Status == "waiting_authorization" || !slices.Contains([]string{"accepted", "stop_rejected"}, current.ExecutorStatus)) {
				return status.Error(codes.PermissionDenied, "executor claim superseded")
			}
			return s.authorize(ctx, current, false)
		}
		if g.ResourceType == "file" {
			m, err := s.readFileMaterial(ctx, input, g.ResourceID)
			if err != nil {
				return nil, err
			}
			if err := checkCurrent(); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"resource_type": "file", "file_id": m.FileID, "name": m.FileName, "mime_type": m.MimeType, "content": m.Content})
		}
		query := req.Query
		if query == "" {
			inputs, err := s.repo.Inputs(ctx, r.ID)
			if err != nil {
				return nil, err
			}
			if len(inputs) > 0 {
				query = inputs[len(inputs)-1].Content
			}
		}
		docs, _, err := s.retrieveResources(ctx, input, query)
		if err != nil {
			return nil, err
		}
		if err := checkCurrent(); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"resource_type": "knowledge", "items": docs})
	}
	if req.Op == "list" {
		combined := []resourcegrant.Grant{}
		for _, kind := range []string{"file", "knowledge"} {
			raw, err := s.manageResource(ctx, user, kind, body)
			if err != nil {
				return nil, err
			}
			var grants []resourcegrant.Grant
			if json.Unmarshal(raw, &grants) != nil {
				return nil, status.Error(codes.Internal, "invalid owner resource response")
			}
			combined = append(combined, grants...)
		}
		return json.Marshal(combined)
	}
	kind := req.ResourceType
	if req.Op == "revoke" {
		for _, candidate := range []string{"file", "knowledge"} {
			var g resourcegrant.Grant
			if s.svc.DB.WithContext(ctx).Table(resourcegrant.Table(candidate)).Where("id = ? AND owner_id = ?", req.GrantID, user).Take(&g).Error == nil {
				kind = candidate
				break
			}
		}
	}
	if !slices.Contains([]string{"file", "knowledge"}, kind) {
		return nil, status.Error(codes.InvalidArgument, "file or knowledge resource required")
	}
	return s.manageResource(ctx, user, kind, body)
}
func (s *Service) manageResource(ctx context.Context, user, kind string, body []byte) ([]byte, error) {
	if kind == "file" {
		if s.files == nil {
			return nil, status.Error(codes.Unavailable, "file service unavailable")
		}
		result, err := s.files.ResourceAuthorization(userContext(ctx, user), &filepb.ResourceAuthorizationReq{Body: body})
		if err != nil {
			return nil, err
		}
		return result.Body, nil
	}
	if s.kb == nil {
		return nil, status.Error(codes.Unavailable, "knowledge service unavailable")
	}
	result, err := s.kb.ResourceAuthorization(userContext(ctx, user), &kb.ResourceAuthorizationReq{Body: body})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func (s *Service) currentRunTools(ctx context.Context, r *model.Run) error {
	var stored int64
	if err := s.svc.DB.WithContext(ctx).Model(&model.Run{}).Where("id = ?", r.ID).Count(&stored).Error; err != nil {
		return err
	}
	if stored == 0 {
		var config struct {
			Tools []resourcegrant.ToolRef `json:"tools"`
		}
		if err := json.Unmarshal(r.Config, &config); err != nil {
			return err
		}
		return resourcegrant.CheckToolPermissions(s.svc.DB.WithContext(ctx), resourceAccess(r), config.Tools, false)
	}
	return resourcegrant.CheckRunToolPermissions(s.svc.DB.WithContext(ctx), resourceAccess(r), r.ID, false)
}
