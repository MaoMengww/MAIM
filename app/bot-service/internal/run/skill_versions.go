package run

import (
	"context"
	"encoding/json"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

func (s *Service) versionAuthority(ctx context.Context, tx *gorm.DB, caller, botID, versionID string, maintain bool, allowExpired ...bool) error {
	target, err := skill.FindVersion(tx, botID, versionID)
	if err != nil {
		return err
	}
	if target.ScopeKey == "bot" {
		var owner struct {
			OwnerID *string
			Status  string
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Table("bots").Where("id = ?", botID).Take(&owner).Error; err != nil {
			return err
		}
		if owner.OwnerID == nil || *owner.OwnerID != caller || owner.Status != "active" {
			return status.Error(codes.PermissionDenied, "current Bot owner required")
		}
		return nil
	}
	state := &candidateState{}
	if err := tx.Table("bot.skill_candidates").Where("published_version_id = ? AND bot_id = ?", versionID, botID).Take(&state.Row).Error; err != nil {
		return err
	}
	if state.Row.SourceReviewID == nil {
		return status.Error(codes.PermissionDenied, "source authority unavailable")
	}
	if err := tx.Where("id = ?", *state.Row.SourceReviewID).Take(&state.Review).Error; err != nil {
		return err
	}
	if err := repo.LockRunConversation(tx, state.Review.ConvID); err != nil {
		return err
	}
	if err := tx.Where("id = ?", state.Review.RunID).Take(&state.Run).Error; err != nil {
		return err
	}
	if err := tx.Where("id = ?", botID).Take(&state.Bot).Error; err != nil {
		return err
	}
	if err := json.Unmarshal(state.Row.ReviewContent, &state.Content); err != nil {
		return err
	}
	if (len(allowExpired) == 0 || !allowExpired[0]) && (!state.Review.ExpiresAt.After(time.Now()) || state.Content.ExpiresAt <= time.Now().Unix()) {
		return status.Error(codes.PermissionDenied, "version source materials expired")
	}
	return s.candidatePrincipal(ctx, tx, state, caller, maintain)
}

func (s *Service) filterVersionState(ctx context.Context, tx *gorm.DB, caller, botID string, view *skill.PackageView) error {
	visible := make(map[string]bool)
	versions := view.Versions[:0]
	for _, version := range view.Versions {
		if err := s.versionAuthority(ctx, tx, caller, botID, version.VersionID, false); err != nil {
			if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.NotFound || status.Code(err) == codes.FailedPrecondition {
				continue
			}
			return err
		}
		visible[version.VersionID] = true
		versions = append(versions, version)
	}
	view.Versions = versions
	if view.EnabledVersion != nil && !visible[view.EnabledVersion.VersionID] {
		view.EnabledVersion = nil
	}
	if view.BlockedUpgrade != nil && !visible[view.BlockedUpgrade.Version.VersionID] {
		view.BlockedUpgrade = nil
	}
	activities := view.Activities[:0]
	for _, activity := range view.Activities {
		if visible[activity.VersionID] {
			activities = append(activities, activity)
		}
	}
	view.Activities = activities
	return nil
}

func (s *Service) SkillVersionOperation(ctx context.Context, caller, botID, op string, body []byte) ([]byte, error) {
	if identity.Validate(caller) != nil || identity.Validate(botID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid identities required")
	}
	var response []byte
	err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if op == "version-list" {
			var scopes []struct {
				SkillID   string
				ScopeKey  string
				VersionID string
			}
			if err := tx.Table("bot.skill_bindings").Where("bot_id = ?", botID).Find(&scopes).Error; err != nil {
				return err
			}
			views := []*skill.PackageView{}
			for _, scope := range scopes {
				if err := s.versionAuthority(ctx, tx, caller, botID, scope.VersionID, false); err != nil {
					if status.Code(err) == codes.PermissionDenied {
						continue
					}
					return err
				}
				manage := s.versionAuthority(ctx, tx, caller, botID, scope.VersionID, true) == nil
				view, err := skill.PackageState(tx, botID, scope.SkillID, scope.ScopeKey, manage)
				if err != nil {
					return err
				}
				if err := s.filterVersionState(ctx, tx, caller, botID, view); err != nil {
					return err
				}
				views = append(views, view)
			}
			var err error
			response, err = json.Marshal(views)
			return err
		}
		action := strings.TrimPrefix(op, "version-")
		if action != "confirm" && action != "rollback" && action != "disable" {
			return status.Error(codes.InvalidArgument, "unknown version action")
		}
		var req skill.VersionRequest
		if json.Unmarshal(body, &req) != nil || identity.Validate(req.VersionID) != nil || req.SubmissionKey == "" || len(req.SubmissionKey) > 256 || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2048 {
			return status.Error(codes.InvalidArgument, "version, digest, revision, bounded reason and submission key required")
		}
		if err := s.versionAuthority(ctx, tx, caller, botID, req.VersionID, true, action == "disable"); err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "version-operation:"+botID+":"+op+":"+req.SubmissionKey).Error; err != nil {
			return err
		}
		var err error
		response, err = candidateReplay(tx, botID, op, req.SubmissionKey, body)
		if err != nil {
			return err
		}
		if response != nil {
			var replay skill.PackageView
			if err := json.Unmarshal(response, &replay); err != nil {
				return err
			}
			if err := s.filterVersionState(ctx, tx, caller, botID, &replay); err != nil {
				return err
			}
			response, err = json.Marshal(&replay)
			return err
		}
		view, err := skill.ChangeVersion(ctx, tx, botID, caller, action, req)
		if err != nil {
			return err
		}
		if err := s.filterVersionState(ctx, tx, caller, botID, view); err != nil {
			return err
		}
		response, err = json.Marshal(view)
		if err != nil {
			return err
		}
		return recordCandidateOperation(tx, botID, op, req.SubmissionKey, body, response)
	})
	return response, err
}
