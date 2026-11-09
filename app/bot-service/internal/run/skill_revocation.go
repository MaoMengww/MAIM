package run

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Validation runs outside the publication transaction. Keep cancellation tied
// to the exact preserved baseline identities, not to the mutable effective head.
// The adapter requests stop and independently verifies cleanup before returning.
func (s *Service) guardCandidateEvidence(parent context.Context, state *candidateState, versions []string) (context.Context, func()) {
	ctx, cancel := context.WithDeadline(parent, time.Unix(state.Content.ExpiresAt, 0))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if archiveCandidate(state) {
					if err := s.archiveAuthority(tx, state); err != nil {
						return err
					}
				} else if err := repo.LockRunAuthority(tx, &state.Run); err != nil {
					return err
				}
				var current governedCandidate
				if err := tx.Where("id = ?", state.Row.ID).Take(&current).Error; err != nil {
					return err
				}
				var evidence reviewCandidate
				if current.Digest != state.Content.Digest || current.Status != "evaluating" || json.Unmarshal(current.ReviewContent, &evidence) != nil || evidence.ExpiresAt <= time.Now().Unix() {
					return status.Error(codes.FailedPrecondition, "candidate evidence changed or expired")
				}
				if len(versions) > 0 {
					var available int64
					if err := tx.Table("bot.skill_versions").Where("id IN ? AND status = 'published'", versions).Count(&available).Error; err != nil {
						return err
					}
					if available != int64(len(versions)) {
						return status.Error(codes.PermissionDenied, "baseline version revoked")
					}
					var retained []governedCandidate
					if err := tx.Where("published_version_id IN ?", versions).Find(&retained).Error; err != nil {
						return err
					}
					if len(retained) != len(versions) {
						return status.Error(codes.FailedPrecondition, "baseline evidence unavailable")
					}
					for _, row := range retained {
						var evidence reviewCandidate
						if json.Unmarshal(row.ReviewContent, &evidence) != nil || row.PublishedVersionID == nil || evidence.ExpiresAt <= time.Now().Unix() && state.Content.baselineSample(*row.PublishedVersionID) == nil {
							return status.Error(codes.FailedPrecondition, "baseline evidence expired")
						}
						if row.SourceReviewID == nil && evidence.Origin == "archive" {
							source := &candidateState{Row: row, Content: evidence, Viewer: state.Viewer}
							if err := s.archiveAuthority(tx, source); err != nil {
								return err
							}
							continue
						}
						if row.SourceReviewID == nil {
							return status.Error(codes.PermissionDenied, "baseline authority unavailable")
						}
						var review runReview
						if err := tx.Where("id = ?", *row.SourceReviewID).Take(&review).Error; err != nil {
							return err
						}
						var source model.Run
						if err := tx.Where("id = ?", review.RunID).Take(&source).Error; err != nil {
							return err
						}
						if err := repo.LockRunAuthority(tx, &source); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
