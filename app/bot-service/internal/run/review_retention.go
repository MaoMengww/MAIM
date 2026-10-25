package run

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Retention is anchored to collection, never the latest read or retry.
func (s *Service) reviewRetentionDeadline(created time.Time) (time.Time, error) {
	days := s.svc.Config.Bot.ReviewRetentionDays
	if days < 1 || days > 30 {
		return time.Time{}, status.Error(codes.FailedPrecondition, "finite review retention between 1 and 30 days required")
	}
	return created.Add(time.Duration(days) * 24 * time.Hour), nil
}

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
	var materials []Material
	if len(r.Materials) > 0 {
		if err := json.Unmarshal(r.Materials, &materials); err != nil {
			return expires, err
		}
	}
	for _, material := range materials {
		if material.ParentRunID != "" {
			versions = append(versions, model.SourceVersion{ParentRunID: material.ParentRunID})
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

// A bounded durable scan is safe to resume after process failure. Neither the
// source Run nor its messages, legitimate output files, or workspace are erased.
func (s *Service) cleanupReviewEvidence(ctx context.Context) {
	cursor := ""
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		var rows []runReview
		query := s.svc.DB.WithContext(work).Order("id").Limit(100)
		if cursor != "" {
			query = query.Where("id > ?", cursor)
		}
		err := query.Find(&rows).Error
		if err == nil {
			for i := range rows {
				if cleanupErr := s.cleanupReviewCopies(work, &rows[i]); cleanupErr != nil {
					s.svc.Logger.Error("Review evidence cleanup failed; durable work will retry")
				}
				cursor = rows[i].ID
			}
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			s.svc.Logger.Error("Review evidence cleanup failed; durable work will retry")
		}
		if err == nil && len(rows) == 100 {
			continue
		}
		cursor = ""
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func (s *Service) cleanupReviewCopies(ctx context.Context, review *runReview) error {
	r, err := s.repo.Get(ctx, review.RunID)
	if err != nil {
		return err
	}
	expires, err := s.reviewEffectiveExpiry(ctx, r, review)
	if err != nil {
		return err
	}
	return s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repo.LockRunConversation(tx, r.ConvID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", r.ID).Take(r).Error; err != nil {
			return err
		}
		var current runReview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", review.ID).Take(&current).Error; err != nil {
			return err
		}
		updates := make(map[string]any)
		if expires.Before(current.ExpiresAt) {
			current.ExpiresAt = expires
			updates["expires_at"] = expires
		}
		revoked := current.EvidenceRevokedAt != nil
		if !revoked {
			err := s.candidateSourceRevocation(ctx, tx, r)
			if err != nil {
				if status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.NotFound {
					return err
				}
				revoked = true
			}
		}
		now := time.Now()
		if revoked && current.EvidenceRevokedAt == nil {
			updates["evidence_revoked_at"] = now
		}
		if (revoked || !current.ExpiresAt.After(now)) && current.MaterialsClearedAt == nil {
			updates["feedback"], updates["assessment"], updates["evidence"] = "", json.RawMessage(`{}`), json.RawMessage(`{}`)
			updates["reason"] = "evidence retention expired; new authorized evidence required"
			if revoked {
				updates["reason"] = "source authorization revoked; materials removed"
			}
			updates["materials_cleared_at"] = now
			if !reviewFinished(current.Status) {
				updates["status"] = "cancelled"
			}
		}
		if len(updates) > 0 {
			if err := tx.Model(&runReview{}).Where("id = ?", current.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		var candidates []governedCandidate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_review_id = ?", current.ID).Find(&candidates).Error; err != nil {
			return err
		}
		for i := range candidates {
			row := &candidates[i]
			var content reviewCandidate
			if err := json.Unmarshal(row.ReviewContent, &content); err != nil {
				return err
			}
			if err := clearCandidateReceipts(tx, row); err != nil {
				return err
			}
			// A renewed identity must not keep responses from its old evidence.
			if !current.ExpiresAt.After(now) {
				if err := tx.Exec(`UPDATE bot.skill_candidate_validations SET result = result - 'reason' WHERE candidate_id = ? AND digest <> ?`, row.ID, row.Digest).Error; err != nil {
					return err
				}
				if err := tx.Table("bot.skill_evaluations").Where("candidate_id = ? AND content_digest <> ?", row.ID, row.Digest).Updates(map[string]any{"reason": "", "observed": ""}).Error; err != nil {
					return err
				}
			}
			if !revoked && content.ExpiresAt > now.Unix() && (content.EvidenceID == "" || content.EvidenceLicenseExpiresAt > now.Unix()) && (content.EvidenceID != "" || current.ExpiresAt.After(now)) {
				continue
			}
			if err := clearCandidateCopies(tx, row, &content); err != nil {
				return err
			}
		}
		return nil
	})
}

// Initial evidence may have a shorter candidate deadline. A later independently
// authorized evidence identity does not renew or depend on the original archive.
func (s *Service) reviewEffectiveExpiry(ctx context.Context, r *model.Run, review *runReview) (time.Time, error) {
	expires := review.ExpiresAt
	configured, err := s.reviewRetentionDeadline(review.CreatedAt)
	if err != nil {
		return expires, err
	}
	if configured.Before(expires) {
		expires = configured
	}
	expires, err = s.reviewExpiry(ctx, r, expires, make(map[string]bool))
	if err != nil {
		return expires, err
	}
	var candidates []governedCandidate
	if err := s.svc.DB.WithContext(ctx).Select("review_content").Where("source_review_id = ?", review.ID).Find(&candidates).Error; err != nil {
		return expires, err
	}
	for _, candidate := range candidates {
		var content reviewCandidate
		if err := json.Unmarshal(candidate.ReviewContent, &content); err != nil {
			return expires, err
		}
		if content.EvidenceID == "" && content.ExpiresAt > 0 && time.Unix(content.ExpiresAt, 0).Before(expires) {
			expires = time.Unix(content.ExpiresAt, 0)
		}
	}
	return expires, nil
}

func clearCandidateCopies(tx *gorm.DB, row *governedCandidate, content *reviewCandidate) error {
	state := "expired"
	if row.Status == "published" || row.Status == "rejected" {
		state = row.Status
	}
	var evaluation *candidateEvaluation
	if content.Evaluation != nil {
		value := *content.Evaluation
		value.Reason = ""
		evaluation = &value
	}
	safe := reviewCandidate{ID: content.ID, SkillID: content.SkillID, SourceRunID: content.SourceRunID, PublisherID: content.PublisherID, ScopeKey: content.ScopeKey, Intent: content.Intent, Digest: content.Digest, BaselineVersionID: content.BaselineVersionID, Status: state, CodeReviewRequired: content.CodeReviewRequired, EvidenceDigest: content.EvidenceDigest, EvidenceID: content.EvidenceID, EvidenceLicenseExpiresAt: content.EvidenceLicenseExpiresAt, EvidenceRevision: content.EvidenceRevision, PublishedVersionID: content.PublishedVersionID, Evaluation: evaluation, Reason: "evidence unavailable; new authorized evidence required"}
	safe.ExpiresAt = content.ExpiresAt
	if state == "published" {
		safe.CodeReviewDigest, safe.CodeReviewIdentity = content.CodeReviewDigest, content.CodeReviewIdentity
	}
	raw, err := json.Marshal(safe)
	if err != nil {
		return err
	}
	updates := map[string]any{"name": "", "description": "", "instructions": "", "resources": json.RawMessage(`{}`), "task": "", "expected_output": "", "failure": "", "review_content": json.RawMessage(raw), "status": state}
	if state != "published" {
		updates["evaluation_digest"], updates["approved_digest"], updates["approved_evaluation_digest"] = "", "", ""
		updates["approved_by"], updates["approved_authorization_version"] = nil, 0
	}
	if err := tx.Model(&governedCandidate{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		return err
	}
	if err := tx.Exec(`UPDATE bot.skill_packages SET name = 'expired-' || id::text WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM bot.skill_versions WHERE skill_id = ?)`, row.SkillID, row.SkillID).Error; err != nil {
		return err
	}
	if err := tx.Table("bot.skill_evaluations").Where("candidate_id = ?", row.ID).Updates(map[string]any{"reason": "", "observed": ""}).Error; err != nil {
		return err
	}
	if err := tx.Exec(`UPDATE bot.skill_candidate_validations SET result = result - 'reason' WHERE candidate_id = ?`, row.ID).Error; err != nil {
		return err
	}
	return nil
}

func clearCandidateReceipts(tx *gorm.DB, row *governedCandidate) error {
	// Idempotency receipts keep identity, never stale private response snapshots.
	return tx.Exec(`UPDATE bot.skill_operations SET response = jsonb_strip_nulls(jsonb_build_object(
		'candidate_id', ?::text, 'version_id', response->'version_id', 'skill_id', response->'skill_id',
		'version', response->'version', 'digest', response->'digest',
		'name', CASE WHEN jsonb_exists(response, 'version_id') THEN response->'name' ELSE NULL END,
		'description', CASE WHEN jsonb_exists(response, 'version_id') THEN response->'description' ELSE NULL END,
		'status', response->'status', 'passed', response->'passed',
		'reason', CASE WHEN jsonb_exists(response, 'passed') THEN 'Validation conclusion retained; original detail removed' ELSE NULL END,
		'task_digest', response->'task_digest', 'expected_digest', response->'expected_digest',
		'output_digest', response->'output_digest', 'evidence_digest', response->'evidence_digest',
		'model_version', response->'model_version', 'environment_digest', response->'environment_digest',
		'baseline_digest', response->'baseline_digest', 'execution_id', response->'execution_id',
		'code_review_identity', response->'code_review_identity',
		'enabled', CASE WHEN jsonb_exists(response, 'version_id') THEN response->'enabled' ELSE NULL END,
		'created_at', response->'created_at'))
		WHERE bot_id = ? AND (operation LIKE ? OR response->>'id' = ? OR response->>'candidate_id' = ?)`, row.ID, row.BotID, "%:"+row.ID, row.ID, row.ID).Error
}
