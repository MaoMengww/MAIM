package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Archive origin carries its own authorized samples, never a fabricated source
// Run. It enters the same review, validation and publication state machine.
func archiveCandidate(state *candidateState) bool {
	return state.Content.Origin == "archive" && state.Content.ScopeKey == "bot"
}

func archivePrincipal(state *candidateState, caller string) error {
	c, bot := &state.Content, &state.Bot
	if !archiveCandidate(state) || bot.Status != "active" || bot.OwnerType != "user" || bot.OwnerID == nil ||
		*bot.OwnerID != caller || c.PublisherID != caller || c.PublisherAuthorizationVersion != bot.AuthorizationVersion {
		return status.Error(codes.PermissionDenied, "current archive publisher authority required; ownership or authorization changed")
	}
	return nil
}

func (s *Service) archiveAuthority(tx *gorm.DB, state *candidateState) error {
	var current model.Bot
	if err := tx.Where("id = ?", state.Row.BotID).Take(&current).Error; err != nil {
		return err
	}
	checked := *state
	checked.Bot = current
	return archivePrincipal(&checked, state.Viewer)
}

func candidateMember(tx *gorm.DB, state *candidateState, caller string) (string, error) {
	if archiveCandidate(state) {
		if err := archivePrincipal(state, caller); err != nil {
			return "", err
		}
		return fmt.Sprintf("bot:%s:%s:%d", state.Bot.ID, caller, state.Bot.AuthorizationVersion), nil
	}
	member, _, err := repo.RunMember(tx, state.Run.ConvID, caller)
	return member, err
}

func (s *Service) importCandidateArchive(ctx context.Context, caller, botID string, body []byte) ([]byte, error) {
	var req struct {
		Archive string `json:"archive"`
		candidateRequest
	}
	if json.Unmarshal(body, &req) != nil || req.SubmissionKey == "" || len(req.SubmissionKey) > 256 || !req.EvidenceAuthorized {
		return nil, status.Error(codes.InvalidArgument, "archive, submission_key and explicit evidence_authorized required")
	}
	pkg, err := skill.ParseArchive(req.Archive)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if len(pkg.Scripts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "fixed-script archive requires scripts/worker.py and execution.json; use /skills for instruction-only packages")
	}
	var response []byte
	err = s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state := &candidateState{Viewer: caller}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", botID).Take(&state.Bot).Error; err != nil {
			return err
		}
		state.Content = reviewCandidate{Origin: "archive", ScopeKey: "bot", PublisherID: caller, PublisherAuthorizationVersion: state.Bot.AuthorizationVersion}
		if err := archivePrincipal(state, caller); err != nil {
			return err
		}
		op := "candidate-import:" + caller
		prior, err := candidateReplay(tx, botID, op, req.SubmissionKey, body)
		if err != nil {
			return err
		}
		if prior != nil {
			var receipt struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(prior, &receipt); err != nil {
				return err
			}
			current, err := s.lockCandidate(ctx, tx, caller, botID, receipt.ID)
			if err != nil {
				return err
			}
			response, err = json.Marshal(candidateView(current))
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-package:"+botID+":"+caller+":"+pkg.Name).Error; err != nil {
			return err
		}
		var row struct{ ID string }
		err = tx.Table("bot.skill_packages").Where("bot_id = ? AND publisher_id = ? AND name = ? AND scope_key = 'bot'", botID, caller, pkg.Name).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row.ID, err = identity.New()
			if err != nil {
				return err
			}
			err = tx.Table("bot.skill_packages").Create(map[string]any{"id": row.ID, "bot_id": botID, "publisher_id": caller, "name": pkg.Name, "license": pkg.License, "scope_key": "bot", "created_at": time.Now()}).Error
		}
		if err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		c := &state.Content
		c.ID, c.SkillID, c.PackageDigest, c.Status, c.Intent = id, row.ID, pkg.Digest, "pending_validation", "new"
		var latest struct {
			ID           string
			Instructions string
		}
		err = tx.Table("bot.skill_versions").Select("id,instructions").Where("skill_id = ? AND scope_key = 'bot'", row.ID).Order("version DESC").Take(&latest).Error
		if err == nil {
			c.BaselineVersionID, c.BaselineInstructions, c.Intent = &latest.ID, latest.Instructions, "modify"
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		state.Row = governedCandidate{ID: id, BotID: botID, SkillID: row.ID, BaselineVersionID: c.BaselineVersionID}
		revision := req.candidateRequest
		revision.Name, revision.Description, revision.Instructions, revision.Resources, revision.Scripts = &pkg.Name, &pkg.Description, &pkg.Instructions, &pkg.Resources, &pkg.Scripts
		revision.ImageDigest, revision.EntryPoint = &pkg.ImageDigest, &pkg.EntryPoint
		if revision.Rationale == nil {
			reason := "Manager-imported fixed-script archive"
			revision.Rationale = &reason
		}
		if err := s.reviseCandidate(state, revision); err != nil {
			return err
		}
		resources, err := json.Marshal(c.Resources)
		if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Table("bot.skill_candidates").Create(map[string]any{"id": id, "skill_id": row.ID, "bot_id": botID, "digest": c.Digest, "name": pkg.Name, "description": pkg.Description, "instructions": c.Instructions, "resources": json.RawMessage(resources), "license": pkg.License, "baseline_version_id": c.BaselineVersionID, "status": c.Status, "task": c.RepresentativeTask, "expected_output": c.ExpectedOutput, "created_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := persistCandidate(tx, state); err != nil {
			return err
		}
		current, err := s.lockCandidate(ctx, tx, caller, botID, id)
		if err != nil {
			return err
		}
		response, err = json.Marshal(candidateView(current))
		if err != nil {
			return err
		}
		return recordCandidateOperation(tx, botID, op, req.SubmissionKey, body, response)
	})
	return response, err
}

func (s *Service) lockArchiveCandidate(ctx context.Context, tx *gorm.DB, caller, botID, id string) (*candidateState, error) {
	state := &candidateState{Viewer: caller}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", botID).Take(&state.Bot).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND bot_id = ? AND source_review_id IS NULL", id, botID).Take(&state.Row).Error; err != nil {
		return nil, err
	}
	if err := json.Unmarshal(state.Row.ReviewContent, &state.Content); err != nil {
		return nil, err
	}
	if err := rejectArchiveLineage(state.Row.ReviewContent); err != nil {
		return nil, err
	}
	c := &state.Content
	if err := archivePrincipal(state, caller); err != nil {
		return nil, err
	}
	baselineMatches := c.BaselineVersionID == nil && state.Row.BaselineVersionID == nil || c.BaselineVersionID != nil && state.Row.BaselineVersionID != nil && *c.BaselineVersionID == *state.Row.BaselineVersionID
	if c.ID != id || c.SkillID != state.Row.SkillID || c.SourceRunID != "" || c.Digest != state.Row.Digest || candidateEvidenceCurrent(state) && c.Digest != candidateContentDigest(c) || c.Instructions != state.Row.Instructions || !baselineMatches {
		return nil, status.Error(codes.FailedPrecondition, "archive candidate identity is inconsistent")
	}
	var pkg struct{ PublisherID, ScopeKey string }
	if err := tx.Table("bot.skill_packages").Where("id = ? AND bot_id = ?", c.SkillID, botID).Take(&pkg).Error; err != nil {
		return nil, err
	}
	if pkg.PublisherID != caller || pkg.ScopeKey != "bot" {
		return nil, status.Error(codes.PermissionDenied, "archive package publisher changed")
	}
	c.Status, c.PublishedVersionID = state.Row.Status, state.Row.PublishedVersionID
	c.CanMaintain = true
	if !candidateEvidenceCurrent(state) {
		return state, nil
	}
	if err := archiveComparison(tx, state); err != nil {
		return nil, err
	}
	c.CanViewMaterials, c.CanReviewCode, c.CanPublish = true, true, true
	var validation candidateValidation
	err := tx.Where("candidate_id = ? AND digest = ?", id, c.Digest).Take(&validation).Error
	if err == nil && validation.Status != "running" {
		if err := json.Unmarshal(validation.Result, &c.Evaluation); err != nil {
			return nil, err
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return state, nil
}

func (s *Service) cleanupArchiveCandidates(ctx context.Context) error {
	var rows []governedCandidate
	if err := s.svc.DB.WithContext(ctx).Select("id,bot_id").Where("source_review_id IS NULL AND review_content->>'origin' = 'archive' AND name <> '' AND (review_content->>'expires_at')::bigint <= ?", time.Now().Unix()).Order("id").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var bot struct{ ID string }
			if err := tx.Table("bots").Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("id = ?", row.BotID).Take(&bot).Error; err != nil {
				return err
			}
			var current governedCandidate
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).Take(&current).Error; err != nil {
				return err
			}
			var content reviewCandidate
			if err := json.Unmarshal(current.ReviewContent, &content); err != nil {
				return err
			}
			if content.ExpiresAt > time.Now().Unix() {
				return nil
			}
			return clearCandidateCopies(tx, &current, &content)
		}); err != nil {
			return err
		}
	}
	return nil
}

func rejectArchiveLineage(raw json.RawMessage) error {
	var content map[string]json.RawMessage
	if err := json.Unmarshal(raw, &content); err != nil {
		return err
	}
	for _, field := range []string{"parent_candidate_id", "source_review_id", "source_version_ids"} {
		if _, exists := content[field]; exists {
			return status.Error(codes.FailedPrecondition, "archive candidates cannot contain source-review lineage")
		}
	}
	return nil
}

// Archives may compare only their own publisher's Bot-global immutable baseline.
// They never inherit or reintegrate Run/review-derived source material.
func archiveComparison(tx *gorm.DB, state *candidateState) error {
	if err := archivePrincipal(state, state.Viewer); err != nil {
		return err
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-publish:"+state.Row.SkillID).Error; err != nil {
		return err
	}
	c := &state.Content
	var latest struct{ ID string }
	err := tx.Table("bot.skill_versions").Select("id").Where("bot_id = ? AND skill_id = ? AND scope_key = 'bot'", state.Row.BotID, state.Row.SkillID).Order("version DESC").Take(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	c.ArchiveBaselineStale = c.BaselineVersionID == nil && err == nil || c.BaselineVersionID != nil && (errors.Is(err, gorm.ErrRecordNotFound) || *c.BaselineVersionID != latest.ID)
	c.BaselineContent = nil
	if c.BaselineVersionID == nil {
		return nil
	}
	var baseline struct {
		Instructions       string
		Resources          json.RawMessage
		ValidationContract json.RawMessage
	}
	err = tx.Table("bot.skill_versions v").Select("v.instructions,v.resources,v.validation_contract").
		Joins("JOIN bot.skill_packages p ON p.id = v.skill_id").
		Joins("JOIN bot.skill_bindings b ON b.bot_id = v.bot_id AND b.skill_id = v.skill_id AND b.scope_key = 'bot' AND b.enabled = true").
		Where("v.id = ? AND v.bot_id = ? AND v.skill_id = ? AND v.scope_key = 'bot' AND v.status = 'published' AND p.bot_id = ? AND p.scope_key = 'bot' AND p.publisher_id = ?", *c.BaselineVersionID, state.Row.BotID, state.Row.SkillID, state.Row.BotID, c.PublisherID).Take(&baseline).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.PermissionDenied, "archive baseline is unavailable or its Bot-global enablement was revoked")
	}
	if err != nil {
		return err
	}
	var contract struct {
		Scripts     map[string]string `json:"scripts"`
		ImageDigest string            `json:"image_digest"`
		EntryPoint  string            `json:"entry_point"`
	}
	if err := json.Unmarshal(baseline.ValidationContract, &contract); err != nil {
		return status.Error(codes.FailedPrecondition, "archive baseline execution contract is unavailable")
	}
	resources := map[string]string{}
	if len(baseline.Resources) > 0 && json.Unmarshal(baseline.Resources, &resources) != nil {
		return status.Error(codes.FailedPrecondition, "archive baseline resources are unavailable")
	}
	c.BaselineContent = &candidateBaselineContent{Instructions: baseline.Instructions, Resources: baseline.Resources, Scripts: contract.Scripts, ImageDigest: contract.ImageDigest, EntryPoint: contract.EntryPoint}
	return nil
}
