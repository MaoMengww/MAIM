package run

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type candidateVersion struct {
	ID                 string
	SkillID            string
	ScopeKey           string
	Status             string
	Digest             string
	Instructions       string
	Resources          json.RawMessage
	ValidationContract json.RawMessage
}

func validateCandidateReintegration(req candidateRequest) error {
	if req.CurrentVersionID == nil || identity.Validate(*req.CurrentVersionID) != nil {
		return status.Error(codes.InvalidArgument, "exact observed current_version_id required")
	}
	if req.Name == nil || req.Description == nil || req.Rationale == nil || req.Instructions == nil || req.Resources == nil || req.Scripts == nil || req.ImageDigest == nil || req.EntryPoint == nil {
		return status.Error(codes.InvalidArgument, "reintegration requires an explicitly resubmitted complete package")
	}
	return nil
}

// A review retains its original candidate. Descendants remain bound to that
// same review, source Run, skill and publisher rather than repointing the review.
func candidateLineage(tx *gorm.DB, state *candidateState) error {
	c := &state.Content
	if state.Review.CandidateID == nil || !sameCandidateVersion(c.ParentCandidateID, state.Row.ParentCandidateID) {
		return status.Error(codes.FailedPrecondition, "candidate source lineage is inconsistent")
	}
	if c.ParentCandidateID == nil {
		if *state.Review.CandidateID != c.ID || len(c.SourceVersionIDs) != 0 {
			return status.Error(codes.FailedPrecondition, "candidate root lineage is inconsistent")
		}
		return nil
	}
	if c.SourceReviewID != state.Review.ID || c.BaselineVersionID == nil || len(c.SourceVersionIDs) == 0 || len(c.SourceVersionIDs) > 16 || !slices.Contains(c.SourceVersionIDs, *c.BaselineVersionID) {
		return status.Error(codes.FailedPrecondition, "candidate integration lineage is inconsistent")
	}
	seen := map[string]bool{c.ID: true}
	parent := c.ParentCandidateID
	for parent != nil {
		if seen[*parent] || len(seen) > 32 {
			return status.Error(codes.FailedPrecondition, "candidate lineage exceeds its bounded ancestry")
		}
		seen[*parent] = true
		var row governedCandidate
		if err := tx.Select("id,bot_id,skill_id,source_review_id,parent_candidate_id").Where("id = ?", *parent).Take(&row).Error; err != nil {
			return status.Error(codes.FailedPrecondition, "candidate ancestor is unavailable")
		}
		if row.BotID != state.Row.BotID || row.SkillID != state.Row.SkillID || row.SourceReviewID == nil || *row.SourceReviewID != state.Review.ID || (row.ParentCandidateID == nil && row.ID != *state.Review.CandidateID) {
			return status.Error(codes.FailedPrecondition, "candidate ancestor belongs to another source")
		}
		parent = row.ParentCandidateID
	}
	return nil
}

func sameCandidateVersion(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// All baseline observation and mutation uses the publication fence. A current
// version can neither advance between the observed-ID check and child INSERT,
// nor between the final publication check and its version INSERT.
func latestCandidateVersion(tx *gorm.DB, state *candidateState) (*candidateVersion, error) {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-publish:"+state.Row.SkillID).Error; err != nil {
		return nil, err
	}
	var version candidateVersion
	err := tx.Table("bot.skill_versions").Select("id,skill_id,scope_key,status,digest").Where("skill_id = ? AND scope_key IN ?", state.Row.SkillID, []string{"bot", state.Content.ScopeKey}).Order("version DESC").Take(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &version, err
}

func (s *Service) candidateComparison(ctx context.Context, tx *gorm.DB, state *candidateState) error {
	c := &state.Content
	latest, err := latestCandidateVersion(tx, state)
	if err != nil {
		return err
	}
	var baseline *candidateBaselineContent
	if c.BaselineVersionID != nil {
		// Original candidates still require their Run's frozen catalog identity.
		if c.ParentCandidateID == nil {
			if _, err := reviewTarget(ctx, tx, &state.Run, &state.Review, *c.BaselineVersionID); err != nil {
				return err
			}
		}
		baseline, err = s.candidateVersionContent(ctx, tx, state, *c.BaselineVersionID)
		if err != nil {
			return err
		}
	}
	var current *candidateBaselineContent
	var currentID *string
	if latest != nil {
		currentID = &latest.ID
		if sameCandidateVersion(currentID, c.BaselineVersionID) {
			current = baseline
		} else {
			current, err = s.candidateVersionContent(ctx, tx, state, latest.ID)
			if err != nil {
				return err
			}
		}
	}
	stale := !sameCandidateVersion(c.BaselineVersionID, currentID)
	c.BaselineContent, c.CurrentContent, c.CurrentVersionID, c.BaselineStale = baseline, current, currentID, &stale
	return nil
}

func (s *Service) candidateVersionContent(ctx context.Context, tx *gorm.DB, state *candidateState, id string) (*candidateBaselineContent, error) {
	var version candidateVersion
	err := tx.Table("bot.skill_versions v").Select("v.id,v.skill_id,v.scope_key,v.status,v.digest,v.instructions,v.resources,v.validation_contract").Joins("JOIN bot.skill_packages p ON p.id = v.skill_id").Where("v.id = ? AND v.skill_id = ? AND v.bot_id = ? AND p.bot_id = ? AND p.publisher_id = ? AND p.scope_key IN ? AND v.scope_key IN ?", id, state.Row.SkillID, state.Row.BotID, state.Row.BotID, state.Content.PublisherID, []string{"bot", state.Content.ScopeKey}, []string{"bot", state.Content.ScopeKey}).Take(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.Error(codes.PermissionDenied, "comparison version is unavailable in this source scope")
	}
	if err != nil {
		return nil, err
	}
	if err := s.candidateVersionAuthority(ctx, tx, state, state.Viewer, &version); err != nil {
		return nil, err
	}
	var contract reviewCandidate
	if err := json.Unmarshal(version.ValidationContract, &contract); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "comparison execution contract is unavailable")
	}
	scripts := contract.Scripts
	if scripts == nil {
		scripts = map[string]string{}
	}
	return &candidateBaselineContent{Instructions: version.Instructions, Resources: version.Resources, Scripts: scripts, ImageDigest: contract.ImageDigest, EntryPoint: contract.EntryPoint}, nil
}

func (s *Service) candidateVersionAuthority(ctx context.Context, tx *gorm.DB, state *candidateState, user string, version *candidateVersion) error {
	if version.Status != "published" {
		return status.Error(codes.PermissionDenied, "comparison skill version is unavailable")
	}
	var enabled int64
	if err := tx.Table("bot.skill_bindings").Where("bot_id = ? AND skill_id = ? AND scope_key = ? AND enabled = true", state.Row.BotID, state.Row.SkillID, version.ScopeKey).Count(&enabled).Error; err != nil {
		return err
	}
	if enabled != 1 {
		return status.Error(codes.PermissionDenied, "comparison skill enablement revoked")
	}
	source := &candidateState{Bot: state.Bot, Viewer: user}
	// Do not lock another candidate after the publication advisory lock.
	// Published execution identity is immutable; scoped authority mutations
	// serialize on the same conversation lock already held by lockCandidate.
	err := tx.Where("bot_id = ? AND skill_id = ? AND published_version_id = ? AND status = 'published'", state.Row.BotID, state.Row.SkillID, version.ID).Take(&source.Row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && version.ScopeKey == "bot" {
		return nil
	}
	if err != nil || source.Row.SourceReviewID == nil {
		// Legacy Bot-global packages do not contain source-scoped review data.
		if err == nil && source.Row.SourceReviewID == nil && version.ScopeKey == "bot" {
			return nil
		}
		return status.Error(codes.PermissionDenied, "comparison source authority is unavailable")
	}
	if err := tx.Where("id = ?", *source.Row.SourceReviewID).Take(&source.Review).Error; err != nil {
		return err
	}
	if err := tx.Where("id = ?", source.Review.RunID).Take(&source.Run).Error; err != nil {
		return err
	}
	if err := json.Unmarshal(source.Row.ReviewContent, &source.Content); err != nil {
		return err
	}
	c := &source.Content
	if source.Run.BotID != state.Row.BotID || source.Run.BindingID != state.Run.BindingID || source.Run.ConvID != state.Run.ConvID || source.Review.BotID != state.Row.BotID || source.Review.ScopeKey != state.Content.ScopeKey || c.ScopeKey != state.Content.ScopeKey || c.PublisherID != state.Content.PublisherID || c.SourceRunID != source.Run.ID || c.ID != source.Row.ID || c.SkillID != state.Row.SkillID || c.Digest != source.Row.Digest || c.Digest != version.Digest || c.EvidenceID == "" && c.EvidenceDigest != source.Review.EvidenceDigest {
		return status.Error(codes.PermissionDenied, "comparison source scope is unavailable")
	}
	if err := candidateLineage(tx, source); err != nil {
		return err
	}
	return s.candidatePrincipal(ctx, tx, source, user, false)
}

func (s *Service) newReintegratedCandidate(state *candidateState, req candidateRequest, current *candidateBaselineContent) (*candidateState, error) {
	if err := validateCandidateReintegration(req); err != nil {
		return nil, err
	}
	if len(state.Content.SourceVersionIDs) >= 16 && !slices.Contains(state.Content.SourceVersionIDs, *req.CurrentVersionID) {
		return nil, status.Error(codes.FailedPrecondition, "candidate integration exceeds its bounded source lineage")
	}
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	child := *state
	c := &child.Content
	parent, baseline := state.Row.ID, *req.CurrentVersionID
	c.ID, c.ParentCandidateID, c.SourceReviewID = id, &parent, state.Review.ID
	c.SourceVersionIDs = slices.Clone(state.Content.SourceVersionIDs)
	if !slices.Contains(c.SourceVersionIDs, baseline) {
		c.SourceVersionIDs = append(c.SourceVersionIDs, baseline)
	}
	c.BaselineVersionID, c.BaselineInstructions, c.Intent = &baseline, current.Instructions, "modify"
	c.PublishedVersionID, c.Evaluation, c.ApprovedMemberID = nil, nil, ""
	c.Status, c.Reason = "pending_validation", ""
	child.Row = governedCandidate{ID: id, BotID: state.Row.BotID, SkillID: c.SkillID, SourceReviewID: state.Row.SourceReviewID, ParentCandidateID: &parent, BaselineVersionID: &baseline}
	invalidateCandidate(&child)
	if err := s.reviseCandidate(&child, req); err != nil {
		return nil, err
	}
	// Renewing samples clears old material fields; this newly authorized
	// immutable baseline remains part of the child's exact execution identity.
	if c.BaselineInstructions != current.Instructions {
		c.BaselineInstructions = current.Instructions
		c.Digest = candidateContentDigest(c)
	}
	child.Row.Digest, child.Row.Status, child.Row.Instructions = c.Digest, c.Status, c.Instructions
	child.Row.Task, child.Row.ExpectedOutput = c.RepresentativeTask, c.ExpectedOutput
	c.BaselineContent, c.CurrentContent, c.CurrentVersionID = current, current, &baseline
	stale := false
	c.BaselineStale = &stale
	c.CanViewMaterials = true
	return &child, nil
}

func (s *Service) reintegrateCandidate(ctx context.Context, tx *gorm.DB, state *candidateState, req candidateRequest) (*candidateState, error) {
	if archiveCandidate(state) {
		return nil, status.Error(codes.FailedPrecondition, "archive candidates require a newly imported complete authorized package, not source reintegration")
	}
	latest, err := latestCandidateVersion(tx, state)
	if err != nil {
		return nil, err
	}
	if latest == nil || req.CurrentVersionID == nil || latest.ID != *req.CurrentVersionID {
		return nil, status.Error(codes.Aborted, "current baseline changed; reload authorized comparison before reintegrating")
	}
	if sameCandidateVersion(state.Row.BaselineVersionID, &latest.ID) {
		return nil, status.Error(codes.FailedPrecondition, "candidate already uses the current baseline")
	}
	current, err := s.candidateVersionContent(ctx, tx, state, latest.ID)
	if err != nil {
		return nil, err
	}
	child, err := s.newReintegratedCandidate(state, req, current)
	if err != nil {
		return nil, err
	}
	stored := child.Content
	stored.CanMaintain, stored.CanViewMaterials, stored.CanReviewCode, stored.CanPublish, stored.Evidence = false, false, false, false, ""
	stored.BaselineContent, stored.CurrentContent, stored.CurrentVersionID, stored.BaselineStale, stored.Evaluation = nil, nil, nil, nil, nil
	content, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	resources, err := json.Marshal(stored.Resources)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	insert := tx.Table("bot.skill_candidates").Clauses(clause.OnConflict{DoNothing: true}).Create(map[string]any{"id": stored.ID, "skill_id": stored.SkillID, "bot_id": state.Row.BotID, "parent_candidate_id": state.Row.ID, "source_review_id": state.Row.SourceReviewID, "digest": stored.Digest, "name": stored.Name, "description": stored.Description, "instructions": stored.Instructions, "resources": json.RawMessage(resources), "license": "private-source-scope", "baseline_version_id": stored.BaselineVersionID, "status": stored.Status, "task": stored.RepresentativeTask, "expected_output": stored.ExpectedOutput, "review_content": json.RawMessage(content), "created_at": now, "updated_at": now})
	if insert.Error != nil {
		return nil, insert.Error
	}
	if insert.RowsAffected != 1 {
		return nil, status.Error(codes.AlreadyExists, "an identical integrated candidate already exists; reuse the original submission")
	}
	return child, nil
}
