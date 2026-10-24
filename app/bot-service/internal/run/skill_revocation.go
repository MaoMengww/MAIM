package run

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
)

// Ordinary publication and rollback never touch a frozen Run. A missing or
// disabled immutable identity, however, revokes it even while the Run is waiting.
func (s *Service) disabledRunSkill(ctx context.Context, r *model.Run) (bool, error) {
	var disabled bool
	err := s.svc.DB.WithContext(ctx).Raw(`SELECT EXISTS (
		SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(?::jsonb->'skills', 'null'::jsonb), '[]'::jsonb)) AS frozen
		LEFT JOIN bot.skill_versions v ON v.id::text = frozen->>'version_id' AND v.bot_id = ?
		WHERE v.id IS NULL OR v.status <> 'published'
	)`, string(r.Config), r.BotID).Scan(&disabled).Error
	return disabled, err
}

// Validation runs outside the publication transaction. Keep cancellation tied
// to the exact preserved baseline identities, not to the mutable effective head.
// The adapter requests stop and independently verifies cleanup before returning.
func (s *Service) guardValidationVersions(parent context.Context, versions []string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	if len(versions) == 0 {
		return ctx, cancel
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			var available int64
			err := s.svc.DB.WithContext(ctx).Table("bot.skill_versions").Where("id IN ? AND status = 'published'", versions).Count(&available).Error
			if err != nil || available != int64(len(versions)) {
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
