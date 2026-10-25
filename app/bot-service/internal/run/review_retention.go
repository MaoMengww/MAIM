package run

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/resourcegrant"
)

// reviewExpiry follows actual accepted source dependencies, including inherited
// work. It only shortens retention; reads/retries never renew source permission.
func (s *Service) reviewExpiry(ctx context.Context, r *model.Run, expires time.Time, visited map[string]bool) (time.Time, error) {
	if visited[r.ID] {
		return expires, nil
	}
	visited[r.ID] = true
	for _, kind := range []string{"file", "knowledge"} {
		if len(r.ResourceGrantIDs) == 0 {
			break
		}
		var grants []resourcegrant.Grant
		if err := s.svc.DB.WithContext(ctx).Table(resourcegrant.Table(kind)).Where("id IN ?", r.ResourceGrantIDs).Find(&grants).Error; err != nil {
			return expires, err
		}
		for _, grant := range grants {
			if grant.ExpiresAt.Before(expires) {
				expires = grant.ExpiresAt
			}
		}
	}
	var versions []model.SourceVersion
	if len(r.Provenance) > 0 {
		if err := json.Unmarshal(r.Provenance, &versions); err != nil {
			return expires, err
		}
	}
	for _, source := range versions {
		if source.ParentRunID == "" {
			continue
		}
		parent, err := s.repo.Get(ctx, source.ParentRunID)
		if err != nil {
			return expires, err
		}
		expires, err = s.reviewExpiry(ctx, parent, expires, visited)
		if err != nil {
			return expires, err
		}
	}
	if r.ParentRunID != nil {
		parent, err := s.repo.Get(ctx, *r.ParentRunID)
		if err != nil {
			return expires, err
		}
		expires, err = s.reviewExpiry(ctx, parent, expires, visited)
		if err != nil {
			return expires, err
		}
	}
	return expires, nil
}

// Only newly collected feedback is covered here. Other review evidence and
// candidate material lifecycle remain owned by their existing retention flow.
func (s *Service) cleanupReviewFeedback(ctx context.Context) {
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.svc.DB.WithContext(work).Exec(`UPDATE bot.run_reviews SET feedback = ''
			WHERE id IN (SELECT id FROM bot.run_reviews
				WHERE feedback <> '' AND expires_at <= NOW()
				ORDER BY expires_at, id LIMIT 1000 FOR UPDATE SKIP LOCKED)`).Error
		cancel()
		if err != nil && ctx.Err() == nil {
			s.svc.Logger.Error("Expired review feedback cleanup failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}
