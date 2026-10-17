package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	pb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func uniqueSearchIDs(ids []string) ([]string, error) {
	for _, id := range ids {
		if identity.Validate(id) != nil {
			return nil, errors.New(errors.CodeInvalidParam, "invalid knowledge search resource_id")
		}
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// An empty grant list is a valid empty scope, not a request for platform data.
func (h *KnowledgeBaseHandler) checkRunKnowledgeAccess(ctx context.Context) (resourcegrant.Access, error) {
	access, _ := resourcegrant.Incoming(ctx)
	for _, id := range []string{access.BotID, access.ConvID, access.BindingID} {
		if identity.Validate(id) != nil {
			return access, domain.ErrForbidden
		}
	}
	if access.Visibility != "personal" && access.Visibility != "conversation" {
		return access, domain.ErrForbidden
	}
	if (access.RequesterID == "" && access.Visibility != "conversation") || (access.RequesterID != "" && identity.Validate(access.RequesterID) != nil) {
		return access, domain.ErrForbidden
	}
	if len(access.GrantIDs) == 1 && access.GrantIDs[0] == "" {
		access.GrantIDs = nil
	}
	if h.DB == nil {
		return access, domain.ErrForbidden
	}
	if err := resourcegrant.CheckIDs(h.DB.WithContext(ctx), access, access.Visibility != "personal", false); err != nil {
		return access, err
	}
	return access, nil
}

func (h *KnowledgeBaseHandler) runKnowledgeBases(ctx context.Context) ([]*domain.KnowledgeBase, error) {
	access, err := h.checkRunKnowledgeAccess(ctx)
	if err != nil {
		return nil, err
	}
	if len(access.GrantIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := h.DB.WithContext(ctx).Table(resourcegrant.Table("knowledge")).
		Where("id IN ?", access.GrantIDs).Distinct("resource_id").Order("resource_id ASC").Pluck("resource_id", &ids).Error; err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "list authorized knowledge bases failed", err)
	}
	kbs := make([]*domain.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		kb, err := h.loadKnowledgeBase(ctx, id, false)
		if err != nil {
			return nil, err
		}
		if kb.Status == "active" && kb.Mode == "rag" {
			kbs = append(kbs, kb)
		}
	}
	return kbs, nil
}

// Target identity only chooses a binding scope. Resource permission is checked
// separately; owning a Bot or participating in a conversation grants no KB read.
func (h *KnowledgeBaseHandler) validateSearchTargets(ctx context.Context, req *pb.RetrieveReq) error {
	for _, id := range []*string{req.BotId, req.ConvId} {
		if id != nil && identity.Validate(*id) != nil {
			return errors.New(errors.CodeInvalidParam, "invalid retrieval target_id")
		}
	}
	if access, run := resourcegrant.Incoming(ctx); run {
		if (req.BotId != nil && *req.BotId != access.BotID) || (req.ConvId != nil && *req.ConvId != access.ConvID) {
			return domain.ErrForbidden
		}
		return nil
	}
	caller := getCallerID(ctx)
	if caller == "" {
		return errors.ErrUnauthorized
	}
	if req.BotId == nil && req.ConvId == nil {
		return nil
	}
	if h.DB == nil {
		return domain.ErrForbidden
	}
	if req.ConvId != nil {
		var member struct{ ID string }
		err := h.DB.WithContext(ctx).Table("messaging.conv_members").
			Where("conv_id = ? AND user_id = ? AND member_type = 'user'", *req.ConvId, caller).Take(&member).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrForbidden
		}
		if err != nil {
			return errors.Wrap(errors.CodeDBError, "check retrieval conversation failed", err)
		}
	}
	if req.BotId != nil {
		var bot struct {
			OwnerType string
			OwnerID   *string
		}
		err := h.DB.WithContext(ctx).Table("bot.bots").Select("owner_type", "owner_id").
			Where("id = ? AND status = 'active'", *req.BotId).Take(&bot).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrForbidden
		}
		if err != nil {
			return errors.Wrap(errors.CodeDBError, "check retrieval bot failed", err)
		}
		if bot.OwnerType == "user" && bot.OwnerID != nil && *bot.OwnerID == caller {
			return nil
		}
		if req.ConvId == nil {
			return domain.ErrForbidden
		}
		var binding struct{ ID string }
		err = h.DB.WithContext(ctx).Table("messaging.conv_bots").
			Where("bot_id = ? AND conv_id = ? AND revoked_at IS NULL", *req.BotId, *req.ConvId).Take(&binding).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrForbidden
		}
		if err != nil {
			return errors.Wrap(errors.CodeDBError, "check retrieval bot participation failed", err)
		}
	}
	return nil
}

func (h *KnowledgeBaseHandler) resolveSearchScope(ctx context.Context, req *pb.RetrieveReq) ([]*domain.KnowledgeBase, []string, error) {
	if err := h.validateSearchTargets(ctx, req); err != nil {
		return nil, nil, err
	}
	kbIDs, err := uniqueSearchIDs(req.KbIds)
	if err != nil {
		return nil, nil, err
	}
	docIDs, err := uniqueSearchIDs(req.DocIds)
	if err != nil {
		return nil, nil, err
	}
	_, run := resourcegrant.Incoming(ctx)
	bounded := run || req.BotId != nil || req.ConvId != nil
	available := make(map[string]*domain.KnowledgeBase)
	if run {
		kbs, err := h.runKnowledgeBases(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, kb := range kbs {
			available[kb.ID] = kb
		}
	} else if bounded {
		for _, target := range []struct {
			kind string
			id   *string
		}{{"bot", req.BotId}, {"conv", req.ConvId}} {
			if target.id == nil {
				continue
			}
			bindings, err := h.KBRepo.ListBindingsByTarget(ctx, target.kind, *target.id)
			if err != nil {
				return nil, nil, errors.Wrap(errors.CodeDBError, "list retrieval bindings failed", err)
			}
			for _, binding := range bindings {
				if _, exists := available[binding.KBID]; exists {
					continue
				}
				kb, err := h.loadKnowledgeBase(ctx, binding.KBID, false)
				if stderrors.Is(err, domain.ErrForbidden) || stderrors.Is(err, domain.ErrKBNotFound) {
					continue
				}
				if err != nil {
					return nil, nil, err
				}
				if kb.Status == "active" && kb.Mode == "rag" {
					available[kb.ID] = kb
				}
			}
		}
	}
	selected := make(map[string]*domain.KnowledgeBase, len(available))
	if len(kbIDs) == 0 {
		for id, kb := range available {
			selected[id] = kb
		}
	} else {
		for _, id := range kbIDs {
			kb, exists := available[id]
			if bounded && !exists {
				return nil, nil, domain.ErrForbidden
			}
			if !exists {
				kb, err = h.loadKnowledgeBase(ctx, id, false)
				if err != nil {
					return nil, nil, err
				}
				if kb.Status != "active" || kb.Mode != "rag" {
					return nil, nil, status.Error(codes.FailedPrecondition, "knowledge base is not searchable")
				}
			}
			selected[id] = kb
		}
	}
	if len(docIDs) > 0 {
		docScope := make(map[string]*domain.KnowledgeBase, len(docIDs))
		for _, id := range docIDs {
			doc, err := h.loadDocument(ctx, id, false)
			if err != nil {
				return nil, nil, err
			}
			kb, exists := selected[doc.KBID]
			if !exists && (bounded || len(kbIDs) > 0) {
				return nil, nil, domain.ErrForbidden
			}
			if !exists {
				kb, err = h.loadKnowledgeBase(ctx, doc.KBID, false)
				if err != nil {
					return nil, nil, err
				}
				if kb.Status != "active" || kb.Mode != "rag" {
					return nil, nil, status.Error(codes.FailedPrecondition, "knowledge base is not searchable")
				}
			}
			docScope[kb.ID] = kb
		}
		selected = docScope
	}
	kbs := make([]*domain.KnowledgeBase, 0, len(selected))
	for _, kb := range selected {
		kbs = append(kbs, kb)
	}
	slices.SortFunc(kbs, func(a, b *domain.KnowledgeBase) int { return strings.Compare(a.ID, b.ID) })
	return kbs, docIDs, nil
}

func (h *KnowledgeBaseHandler) authorizeSearchScope(ctx context.Context, req *pb.RetrieveReq, kbs []*domain.KnowledgeBase) error {
	if err := h.validateSearchTargets(ctx, req); err != nil {
		return err
	}
	if _, run := resourcegrant.Incoming(ctx); run {
		if _, err := h.checkRunKnowledgeAccess(ctx); err != nil {
			return err
		}
	}
	for _, kb := range kbs {
		current, err := h.loadKnowledgeBase(ctx, kb.ID, false)
		if err != nil {
			return err
		}
		if current.Status != "active" || current.Mode != "rag" {
			return domain.ErrForbidden
		}
	}
	return nil
}

// Source JSON preserves actual metadata and distinguishes a missing location
// from a real zero-valued parser location. Marshal failures must not fake data.
func sourceMetadataJSON(meta map[string]any) (string, error) {
	if len(meta) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", errors.Wrap(errors.CodeInternal, "encode knowledge source metadata failed", err)
	}
	return string(raw), nil
}
