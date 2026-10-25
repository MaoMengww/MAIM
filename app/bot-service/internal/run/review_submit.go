package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The caller names one accepted Run, never an inferred related conversation or
// a historical scan. The receipt records identity only; replay resolves current
// authorized content rather than returning a stale private response snapshot.
func (s *Service) submitRunReview(ctx context.Context, caller, botID string, request reviewScopeRequest) ([]byte, error) {
	if identity.Validate(caller) != nil {
		return nil, status.Error(codes.Unauthenticated, "authenticated requester required")
	}
	if identity.Validate(botID) != nil || identity.Validate(request.RunID) != nil || len(request.SubmissionKey) == 0 || len(request.SubmissionKey) > 256 || !utf8.ValidString(request.Feedback) {
		return nil, status.Error(codes.InvalidArgument, "valid Run and submission_key required")
	}
	if request.TriggerKind != "feedback" && request.TriggerKind != "historical" || len(request.Feedback) > 8192 || request.TriggerKind == "feedback" && strings.TrimSpace(request.Feedback) == "" || request.TriggerKind == "historical" && request.Feedback != "" {
		return nil, status.Error(codes.InvalidArgument, "explicit historical request or bounded feedback required")
	}
	if request.ParentReviewID != "" && identity.Validate(request.ParentReviewID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid parent review required")
	}
	r, err := s.repo.Get(ctx, request.RunID)
	if err != nil || r.BotID != botID {
		return nil, status.Error(codes.NotFound, "Run not found")
	}
	source := runReview{PersonalUserID: r.PersonalUserID}
	if err := s.reviewViewer(ctx, caller, r, &source); err != nil {
		return nil, err
	}
	if !repo.TerminalRun(r) || r.TriggerKind == "review" || r.TriggerKind == "validation" {
		return nil, status.Error(codes.FailedPrecondition, "only original terminal Runs can be reviewed")
	}
	requestHash := mustReviewDigest(map[string]string{"run_id": r.ID, "trigger_kind": request.TriggerKind, "feedback": request.Feedback, "parent_review_id": request.ParentReviewID})
	operation := "review-submit:" + r.ID + ":" + caller
	var result runReview
	err = s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repo.LockRunConversation(tx, r.ConvID); err != nil {
			return err
		}
		scope := reviewScopeKey(r.BindingID, r.Visibility, caller)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "review-policy:"+scope).Error; err != nil {
			return err
		}
		var current model.Run
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ?", r.ID, botID).Take(&current).Error; err != nil {
			return err
		}
		if err := repo.LockRunAuthority(tx, &current); err != nil {
			return err
		}
		if err := s.candidateMaterialAccess(ctx, tx, &current, caller, make(map[string]bool)); err != nil {
			return err
		}
		var policy reviewPolicy
		err := tx.Where("bot_id = ? AND scope_key = ?", botID, scope).Take(&policy).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && !policy.Enabled {
			return status.Error(codes.FailedPrecondition, "scope review disabled")
		}
		var receipt struct {
			RequestHash string
			Response    json.RawMessage
		}
		err = tx.Table("bot.skill_operations").Where("bot_id = ? AND operation = ? AND submission_key = ?", botID, operation, request.SubmissionKey).Take(&receipt).Error
		if err == nil {
			if receipt.RequestHash != requestHash {
				return status.Error(codes.AlreadyExists, "submission key has different content")
			}
			var recorded struct {
				ReviewID string `json:"review_id"`
			}
			if err := json.Unmarshal(receipt.Response, &recorded); err != nil {
				return err
			}
			return tx.Where("id = ? AND run_id = ?", recorded.ReviewID, r.ID).Take(&result).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var previous runReview
		err = tx.Where("run_id = ?", r.ID).Order("revision DESC").Take(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var parent *string
		if request.ParentReviewID != "" {
			var selected runReview
			if err := tx.Where("id = ? AND run_id = ? AND bot_id = ? AND scope_key = ?", request.ParentReviewID, r.ID, botID, scope).Take(&selected).Error; err != nil {
				return status.Error(codes.InvalidArgument, "parent review must belong to this Run")
			}
			parent = &selected.ID
		} else if previous.ID != "" {
			parent = &previous.ID
		}
		// A new intent does not renew permission to the old evidence. When no
		// automatic review exists, use the original terminal fact's timestamp.
		expires, err := s.reviewRetentionDeadline(current.UpdatedAt)
		if err != nil {
			return err
		}
		var oldest struct{ ExpiresAt time.Time }
		if err := tx.Model(&runReview{}).Select("MIN(expires_at) AS expires_at").Where("run_id = ?", r.ID).Scan(&oldest).Error; err != nil {
			return err
		}
		if !oldest.ExpiresAt.IsZero() && oldest.ExpiresAt.Before(expires) {
			expires = oldest.ExpiresAt
		}
		expires, err = s.reviewExpiry(ctx, &current, expires, make(map[string]bool))
		if err != nil {
			return err
		}
		var publisher model.Bot
		if err := tx.Where("id = ?", botID).Take(&publisher).Error; err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		now := time.Now()
		result = runReview{ID: id, RunID: r.ID, BotID: botID, ConvID: current.ConvID, BindingID: current.BindingID, ScopeKey: scope, PersonalUserID: current.PersonalUserID, PublisherID: publisher.OwnerID, Revision: previous.Revision + 1, TriggerKind: request.TriggerKind, SubmittedBy: &caller, ParentReviewID: parent, Feedback: request.Feedback, Status: "queued", Assessment: json.RawMessage(`{}`), Evidence: json.RawMessage(`{}`), ExpiresAt: expires, CreatedAt: now, UpdatedAt: now}
		if request.TriggerKind == "feedback" {
			result.EvidenceDigest = mustReviewDigest(request.Feedback)
		}
		if !expires.After(now) {
			result.Status, result.Reason = "no_candidate", "original evidence expired; new authorized evidence required"
			result.Feedback = ""
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if result.Status == "queued" {
			outboxID, err := identity.New()
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"review_id": id})
			if err := tx.Create(&model.RunOutbox{ID: outboxID, RunID: r.ID, Kind: "review", Version: result.Revision, Payload: payload, AvailableAt: now}).Error; err != nil {
				return err
			}
		}
		receiptID, err := identity.New()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]string{"review_id": id})
		return tx.Table("bot.skill_operations").Create(map[string]any{"id": receiptID, "bot_id": botID, "operation": operation, "submission_key": request.SubmissionKey, "request_hash": requestHash, "response": json.RawMessage(raw), "created_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	if err := s.reviewViewer(ctx, caller, r, &result); err != nil {
		return nil, err
	}
	view, err := s.reviewView(ctx, caller, &result, true)
	if err != nil {
		return nil, err
	}
	return json.Marshal(view)
}
