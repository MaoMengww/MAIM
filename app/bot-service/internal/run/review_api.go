package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func reviewScopeKey(binding, visibility, user string) string {
	if visibility == "personal" {
		return "personal:" + user + ":" + binding
	}
	return "conversation:" + binding
}

func (s *Service) reviewScope(ctx context.Context, caller, botID string, request reviewScopeRequest) (*reviewPolicy, bool, error) {
	if identity.Validate(caller) != nil {
		return nil, false, status.Error(codes.Unauthenticated, "authenticated requester required")
	}
	if identity.Validate(botID) != nil || identity.Validate(request.ConvID) != nil || (request.Visibility != "conversation" && request.Visibility != "personal") {
		return nil, false, status.Error(codes.InvalidArgument, "valid conversation and visibility required")
	}
	_, role, err := repo.RunMember(s.svc.DB.WithContext(ctx), request.ConvID, caller)
	if err != nil {
		return nil, false, err
	}
	var binding model.ConvBot
	if err := s.svc.DB.WithContext(ctx).Where("bot_id = ? AND conv_id = ? AND revoked_at IS NULL", botID, request.ConvID).Take(&binding).Error; err != nil {
		return nil, false, status.Error(codes.PermissionDenied, "current Bot binding required")
	}
	policy := &reviewPolicy{BotID: botID, BindingID: binding.ID, ScopeKey: reviewScopeKey(binding.ID, request.Visibility, caller), Enabled: true}
	if request.Visibility == "personal" {
		policy.PersonalUserID = &caller
	}
	err = s.svc.DB.WithContext(ctx).Where("bot_id = ? AND scope_key = ?", botID, policy.ScopeKey).Take(policy).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	return policy, request.Visibility == "personal" || role <= 2, nil
}

func policyView(policy *reviewPolicy, manage bool) map[string]any {
	return map[string]any{"enabled": policy.Enabled, "scope_key": policy.ScopeKey, "revision": policy.Revision, "can_manage": manage}
}

// ReviewOperation authenticates source scope independently of Bot ownership.
func (s *Service) ReviewOperation(ctx context.Context, caller, botID, operation string, body []byte) ([]byte, error) {
	if len(body) > 1<<20 {
		return nil, status.Error(codes.InvalidArgument, "review request too large")
	}
	var request reviewScopeRequest
	if json.Unmarshal(body, &request) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid review request")
	}
	if operation == "review-submit" {
		return s.submitRunReview(ctx, caller, botID, request)
	}
	if operation == "review-get" || operation == "review-cancel" {
		if identity.Validate(request.ReviewID) != nil {
			return nil, status.Error(codes.InvalidArgument, "valid review identity required")
		}
		var review runReview
		if err := s.svc.DB.WithContext(ctx).Where("id = ? AND bot_id = ?", request.ReviewID, botID).Take(&review).Error; err != nil {
			return nil, status.Error(codes.NotFound, "review not found")
		}
		r, err := s.repo.Get(ctx, review.RunID)
		if err != nil {
			return nil, err
		}
		if err := s.reviewViewer(ctx, caller, r, &review); err != nil {
			return nil, err
		}
		if operation == "review-cancel" {
			if len(request.SubmissionKey) == 0 || len(request.SubmissionKey) > 256 {
				return nil, status.Error(codes.InvalidArgument, "submission_key required")
			}
			err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := repo.LockRunConversation(tx, r.ConvID); err != nil {
					return err
				}
				var current model.Run
				if err := tx.Where("id = ?", r.ID).Take(&current).Error; err != nil {
					return err
				}
				if err := repo.RunController(tx, &current, caller); err != nil {
					return err
				}
				if err := repo.CheckRunSources(tx, &current); err != nil {
					return err
				}
				return tx.Model(&runReview{}).Where("id = ? AND status IN ?", review.ID, []string{"queued", "assessing", "generating"}).Updates(map[string]any{"status": "cancelled", "reason": "cancelled by authorized user", "updated_at": time.Now()}).Error
			})
			if err != nil {
				return nil, err
			}
			if err := s.svc.DB.WithContext(ctx).Where("id = ?", review.ID).Take(&review).Error; err != nil {
				return nil, err
			}
		}
		view, err := s.reviewView(ctx, caller, &review, true)
		if err != nil {
			return nil, err
		}
		return json.Marshal(view)
	}
	policy, manage, err := s.reviewScope(ctx, caller, botID, request)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "review-policy-get":
		return json.Marshal(policyView(policy, manage))
	case "review-policy-set":
		if !manage {
			return nil, status.Error(codes.PermissionDenied, "scope administrator permission required")
		}
		if request.Enabled == nil || len(request.SubmissionKey) == 0 || len(request.SubmissionKey) > 256 {
			return nil, status.Error(codes.InvalidArgument, "enabled and submission_key required")
		}
		err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := repo.LockRunConversation(tx, request.ConvID); err != nil {
				return err
			}
			_, role, err := repo.RunMember(tx, request.ConvID, caller)
			if err != nil {
				return err
			}
			if request.Visibility != "personal" && role > 2 {
				return status.Error(codes.PermissionDenied, "scope administrator permission required")
			}
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "review-policy:"+policy.ScopeKey).Error; err != nil {
				return err
			}
			operation := "review-policy:" + policy.ScopeKey + ":" + caller
			requestHash, err := hash(request)
			if err != nil {
				return err
			}
			var prior struct {
				RequestHash string
				Response    json.RawMessage
			}
			err = tx.Table("bot.skill_operations").Where("bot_id = ? AND operation = ? AND submission_key = ?", botID, operation, request.SubmissionKey).Take(&prior).Error
			if err == nil {
				if prior.RequestHash != requestHash {
					return status.Error(codes.AlreadyExists, "submission key has different content")
				}
				return json.Unmarshal(prior.Response, policy)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			now := time.Now()
			policy.Enabled, policy.UpdatedBy, policy.UpdatedAt = *request.Enabled, caller, now
			policy.Revision = 1
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "bot_id"}, {Name: "scope_key"}}, DoUpdates: clause.Assignments(map[string]any{"enabled": policy.Enabled, "revision": gorm.Expr("run_review_policies.revision + CASE WHEN run_review_policies.enabled = ? THEN 0 ELSE 1 END", policy.Enabled), "updated_by": caller, "updated_at": now})}).Create(policy).Error; err != nil {
				return err
			}
			if !policy.Enabled {
				if err := tx.Model(&runReview{}).Where("bot_id = ? AND scope_key = ? AND status IN ?", botID, policy.ScopeKey, []string{"queued", "assessing", "generating"}).Updates(map[string]any{"status": "cancelled", "reason": "scope review disabled", "updated_at": now}).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("bot_id = ? AND scope_key = ?", botID, policy.ScopeKey).Take(policy).Error; err != nil {
				return err
			}
			raw, err := json.Marshal(policy)
			if err != nil {
				return err
			}
			id, err := identity.New()
			if err != nil {
				return err
			}
			return tx.Table("bot.skill_operations").Create(map[string]any{"id": id, "bot_id": botID, "operation": operation, "submission_key": request.SubmissionKey, "request_hash": requestHash, "response": json.RawMessage(raw), "created_at": now}).Error
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(policyView(policy, manage))
	case "review-list":
		var rows []runReview
		if err := s.svc.DB.WithContext(ctx).Where("bot_id = ? AND scope_key = ?", botID, policy.ScopeKey).Order("created_at DESC,id DESC").Limit(100).Find(&rows).Error; err != nil {
			return nil, err
		}
		views := make([]reviewView, 0, len(rows))
		for i := range rows {
			r, err := s.repo.Get(ctx, rows[i].RunID)
			if err != nil {
				return nil, err
			}
			if err := s.reviewViewer(ctx, caller, r, &rows[i]); err != nil {
				if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.NotFound {
					continue
				}
				return nil, err
			}
			view, err := s.reviewView(ctx, caller, &rows[i], false)
			if err != nil {
				return nil, err
			}
			if !policy.Enabled {
				view.Reason = "scope review disabled; materials hidden"
			}
			views = append(views, *view)
		}
		return json.Marshal(views)
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown review operation")
	}
}

func (s *Service) reviewViewer(ctx context.Context, caller string, r *model.Run, review *runReview) error {
	if identity.Validate(caller) != nil {
		return status.Error(codes.Unauthenticated, "authenticated requester required")
	}
	// After expiry, a current scope maintainer can inspect safe identities even
	// when the old grant's natural TTL prevents reading its former materials.
	if review.ID != "" {
		expires, err := s.reviewEffectiveExpiry(ctx, r, review)
		if err != nil {
			return err
		}
		if !expires.After(time.Now()) || review.MaterialsClearedAt != nil {
			return s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := s.candidateSourceRevocation(ctx, tx, r); err != nil {
					return err
				}
				_, role, err := repo.RunMember(tx, r.ConvID, caller)
				if err != nil {
					return err
				}
				if r.Visibility == "personal" {
					if review.PersonalUserID == nil || *review.PersonalUserID != caller {
						return status.Error(codes.PermissionDenied, "personal review is private")
					}
				} else if role > 2 {
					return status.Error(codes.PermissionDenied, "scope maintainer required for expired review metadata")
				}
				return nil
			})
		}
	}
	if err := s.visible(ctx, caller, r); err != nil {
		return err
	}
	// Current source authorization, not Bot ownership, gates derived content too.
	if err := s.authorize(ctx, r, false); err != nil {
		return err
	}
	if err := repo.CheckRunSources(s.svc.DB.WithContext(ctx), r); err != nil {
		return err
	}
	if r.Visibility == "personal" && (review.PersonalUserID == nil || *review.PersonalUserID != caller) {
		return status.Error(codes.PermissionDenied, "personal review is private")
	}
	return s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.candidateMaterialAccess(ctx, tx, r, caller, make(map[string]bool))
	})
}

func (s *Service) reviewView(ctx context.Context, caller string, review *runReview, detail bool) (*reviewView, error) {
	r, err := s.repo.Get(ctx, review.RunID)
	if err != nil {
		return nil, err
	}
	expires, err := s.reviewEffectiveExpiry(ctx, r, review)
	if err != nil {
		return nil, err
	}
	current := expires.After(time.Now()) && review.MaterialsClearedAt == nil && review.EvidenceRevokedAt == nil
	view := &reviewView{ID: review.ID, RunID: review.RunID, BotID: review.BotID, ConvID: review.ConvID, ScopeKey: review.ScopeKey, Status: review.Status, Reason: review.Reason, ModelCalls: review.ModelCalls, ModelVersion: review.ModelVersion, CandidateID: review.CandidateID, CreatedAt: review.CreatedAt.Unix(), UpdatedAt: review.UpdatedAt.Unix(), ExpiresAt: review.ExpiresAt.Unix()}
	view.Revision, view.TriggerKind, view.SubmittedBy, view.ParentReviewID = review.Revision, review.TriggerKind, review.SubmittedBy, review.ParentReviewID
	view.ExpiresAt = expires.Unix()
	if !current {
		view.Reason = "evidence retention expired; new authorized evidence required"
		return view, nil
	}
	if detail {
		var policy reviewPolicy
		err := s.svc.DB.WithContext(ctx).Where("bot_id = ? AND scope_key = ?", review.BotID, review.ScopeKey).Take(&policy).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil && !policy.Enabled {
			view.Reason = "scope review disabled; materials hidden"
			return view, nil
		}
	}
	if detail && review.ExpiresAt.After(time.Now()) {
		view.Feedback = review.Feedback
		if len(review.Assessment) > 2 {
			var assessment reviewAssessment
			if err := json.Unmarshal(review.Assessment, &assessment); err != nil {
				return nil, err
			}
			view.Assessment = &assessment
		}
	}
	if detail && review.CandidateID != nil && review.ExpiresAt.After(time.Now()) {
		body, _ := json.Marshal(map[string]string{"candidate_id": *review.CandidateID})
		current, err := s.CandidateOperation(ctx, caller, review.BotID, "candidate-get", body)
		if err != nil {
			return nil, err
		}
		var candidate reviewCandidate
		if err := json.Unmarshal(current, &candidate); err != nil {
			return nil, err
		}
		view.Candidate = &candidate
	}
	// Check again after resolving a nested candidate: no response may cross the
	// evidence deadline while carrying the old feedback or assessment.
	if !expires.After(time.Now()) {
		view.Reason = "evidence retention expired; new authorized evidence required"
		view.Feedback, view.Assessment, view.Candidate = "", nil, nil
	}
	return view, nil
}

func (s *Service) reviewBoundary(ctx context.Context, r *model.Run, review *runReview) error {
	var current runReview
	if err := s.svc.DB.WithContext(ctx).Where("id = ?", review.ID).Take(&current).Error; err != nil {
		return err
	}
	if current.Status == "cancelled" {
		return status.Error(codes.Canceled, "review cancelled")
	}
	var policy reviewPolicy
	err := s.svc.DB.WithContext(ctx).Where("bot_id = ? AND scope_key = ?", r.BotID, review.ScopeKey).Take(&policy).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil && !policy.Enabled {
		return status.Error(codes.Canceled, "scope review disabled")
	}
	expires, err := s.reviewEffectiveExpiry(ctx, r, &current)
	if err != nil {
		return err
	}
	if !expires.After(time.Now()) || current.MaterialsClearedAt != nil || current.EvidenceRevokedAt != nil {
		return status.Error(codes.PermissionDenied, "review evidence expired")
	}
	if !repo.TerminalRun(r) || strings.EqualFold(r.TriggerKind, "review") || r.TriggerKind == "validation" {
		return status.Error(codes.FailedPrecondition, "only original terminal Runs can be reviewed")
	}
	if err := s.authorize(ctx, r, false); err != nil {
		return err
	}
	if err := repo.CheckRunSources(s.svc.DB.WithContext(ctx), r); err != nil {
		return err
	}
	if review.SubmittedBy != nil {
		return s.reviewViewer(ctx, *review.SubmittedBy, r, review)
	}
	return nil
}
