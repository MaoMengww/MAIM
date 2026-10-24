package run

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func (s *Service) scopedSkillAccess(ctx context.Context, botID, user, scopeKey string, frozen skill.Frozen) error {
	if frozen.ScopeKey == "" || frozen.ScopeKey == "bot" {
		return nil
	}
	if frozen.ScopeKey != scopeKey {
		return status.Error(codes.PermissionDenied, "skill source scope does not match accepted Run")
	}
	return s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row struct{ ID string }
		if err := tx.Table("bot.skill_candidates").Select("id").Where("published_version_id = ? AND bot_id = ? AND status = 'published' AND source_review_id IS NOT NULL", frozen.VersionID, botID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.PermissionDenied, "scoped skill source is unavailable")
			}
			return err
		}
		state, err := s.lockCandidate(ctx, tx, user, botID, row.ID)
		if err != nil {
			return err
		}
		if state.Content.ScopeKey != scopeKey || state.Content.ExpiresAt <= time.Now().Unix() || !state.Review.ExpiresAt.After(time.Now()) {
			return status.Error(codes.PermissionDenied, "scoped skill evidence expired or changed")
		}
		return nil
	})
}

func (s *Service) freezeScopedSkills(ctx context.Context, input Input) ([]skill.Frozen, error) {
	scopeKey := reviewScopeKey(input.BindingID, input.Visibility, input.RequesterID)
	frozen, err := skill.FreezeScoped(ctx, s.svc.DB.DB, input.BotID, input.SkillVersionIDs, scopeKey)
	if err != nil {
		return nil, err
	}
	authorized := make([]skill.Frozen, 0, len(frozen))
	for _, entry := range frozen {
		if err := s.scopedSkillAccess(ctx, input.BotID, input.RequesterID, scopeKey, entry); err != nil {
			if !entry.Explicit && (status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.NotFound || status.Code(err) == codes.FailedPrecondition) {
				continue
			}
			return nil, err
		}
		authorized = append(authorized, entry)
	}
	return authorized, nil
}

func (s *Service) checkScopedRunSkills(ctx context.Context, r *model.Run, user string) error {
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return err
	}
	scopeKey := reviewScopeKey(r.BindingID, r.Visibility, user)
	for _, entry := range config.Skills {
		if err := s.scopedSkillAccess(ctx, r.BotID, user, scopeKey, entry); err != nil {
			return err
		}
	}
	return nil
}
