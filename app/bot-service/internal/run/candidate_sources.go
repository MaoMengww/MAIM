package run

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A scoped version is itself source material. Deriving another candidate from
// a Run whose catalog included it cannot erase that material's authorization
// or retention deadline. Traverse only immutable, older version identities.
func (s *Service) candidateFrozenSources(ctx context.Context, tx *gorm.DB, r *model.Run, user string, visited map[string]bool) error {
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return err
	}
	scopeKey := reviewScopeKey(r.BindingID, r.Visibility, user)
	for _, frozen := range config.Skills {
		if frozen.ScopeKey == "" || frozen.ScopeKey == "bot" {
			continue
		}
		if frozen.ScopeKey != scopeKey {
			return status.Error(codes.PermissionDenied, "frozen skill source is outside this scope")
		}
		if visited[frozen.VersionID] {
			continue
		}
		if len(visited) >= 16 {
			return status.Error(codes.PermissionDenied, "source skill ancestry exceeds the bounded authorization budget")
		}
		visited[frozen.VersionID] = true
		if err := skill.Check(ctx, tx, r.BotID, frozen); err != nil {
			return err
		}
		var candidate governedCandidate
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("bot_id = ? AND published_version_id = ? AND status = 'published'", r.BotID, frozen.VersionID).Take(&candidate).Error; err != nil {
			return status.Error(codes.PermissionDenied, "frozen skill source is unavailable")
		}
		if candidate.SourceReviewID == nil || candidate.SkillID != frozen.PackageID || candidate.Digest != frozen.Digest {
			return status.Error(codes.PermissionDenied, "frozen source identity changed")
		}
		var content reviewCandidate
		if err := json.Unmarshal(candidate.ReviewContent, &content); err != nil {
			return err
		}
		if content.ScopeKey != scopeKey {
			return status.Error(codes.PermissionDenied, "frozen skill source scope changed")
		}
		var review runReview
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", *candidate.SourceReviewID).Take(&review).Error; err != nil {
			return err
		}
		if content.EvidenceID == "" && content.EvidenceDigest != review.EvidenceDigest {
			return status.Error(codes.PermissionDenied, "frozen skill evidence identity changed")
		}
		var source model.Run
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", review.RunID).Take(&source).Error; err != nil {
			return err
		}
		if source.BotID != r.BotID || source.BindingID != r.BindingID || source.ConvID != r.ConvID || source.AccessRevoked || source.ResultInvalidated {
			return status.Error(codes.PermissionDenied, "frozen skill source authority changed")
		}
		if err := s.candidateSourceRevocation(ctx, tx, &source); err != nil {
			return err
		}
		if source.Visibility == "personal" && (source.PersonalUserID == nil || *source.PersonalUserID != user) {
			return status.Error(codes.PermissionDenied, "personal frozen skill source is private")
		}
		if _, _, err := repo.RunMember(tx, source.ConvID, user); err != nil {
			return err
		}
		if err := s.candidateFrozenSources(ctx, tx, &source, user, visited); err != nil {
			return err
		}
		if len(content.SourceVersionIDs) > 0 {
			// Publication does not erase the material ancestry explicitly added
			// by reintegration, even when a later Run freezes this child version.
			state := &candidateState{Row: candidate, Content: content, Review: review, Run: source, Viewer: user}
			if err := tx.Where("id = ?", r.BotID).Take(&state.Bot).Error; err != nil {
				return err
			}
			if err := s.candidateIntegratedSources(ctx, tx, state, user, make(map[string]bool)); err != nil {
				return err
			}
		}
	}
	return nil
}
