package run

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"github.com/maomeng/aim/app/bot-service/internal/skillvalidation"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	kb "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type governedCandidate struct {
	ID                       string `gorm:"type:uuid;primaryKey"`
	BotID                    string
	SkillID                  string
	SourceReviewID           *string
	Digest                   string
	Status                   string
	Instructions             string
	Task                     string
	ExpectedOutput           string
	Resources                json.RawMessage `gorm:"type:jsonb"`
	ReviewContent            json.RawMessage `gorm:"type:jsonb"`
	BaselineVersionID        *string
	PublishedVersionID       *string
	EvaluationDigest         string
	ApprovedDigest           string
	ApprovedEvaluationDigest string
	ApprovedBy               *string
}

func (governedCandidate) TableName() string { return "bot.skill_candidates" }

type candidateEvaluation struct {
	Status             string `json:"status"`
	Passed             bool   `json:"passed"`
	NotExecuted        bool   `json:"not_executed,omitempty"`
	Reason             string `json:"reason"`
	Digest             string `json:"digest"`
	TaskDigest         string `json:"task_digest"`
	ExpectedDigest     string `json:"expected_digest"`
	OutputDigest       string `json:"output_digest"`
	ModelVersion       string `json:"model_version"`
	EnvironmentDigest  string `json:"environment_digest,omitempty"`
	BaselineDigest     string `json:"baseline_digest,omitempty"`
	EvidenceDigest     string `json:"evidence_digest"`
	ExecutionID        string `json:"execution_id,omitempty"`
	CodeReviewIdentity string `json:"code_review_identity,omitempty"`
	CreatedAt          int64  `json:"created_at"`
}

type candidateValidation struct {
	CandidateID  string `gorm:"type:uuid;primaryKey"`
	Digest       string `gorm:"primaryKey"`
	ExecutionKey string
	Status       string
	Result       json.RawMessage `gorm:"type:jsonb"`
	CreatedAt    time.Time
}

func (candidateValidation) TableName() string { return "bot.skill_candidate_validations" }

type candidateRequest struct {
	CandidateID            string                     `json:"candidate_id"`
	SubmissionKey          string                     `json:"submission_key"`
	Digest                 string                     `json:"digest"`
	Reason                 string                     `json:"reason"`
	Confirmed              bool                       `json:"confirmed"`
	Instructions           *string                    `json:"instructions"`
	Resources              *map[string]string         `json:"resources"`
	Scripts                *map[string]string         `json:"scripts"`
	ImageDigest            *string                    `json:"image_digest"`
	EntryPoint             *string                    `json:"entry_point"`
	Task                   *string                    `json:"representative_task"`
	ExpectedOutput         *string                    `json:"expected_output"`
	InputBase64            *string                    `json:"input_b64"`
	ExpectedOutputBase64   *string                    `json:"expected_output_b64"`
	ExpiresAt              *int64                     `json:"expires_at"`
	EvidenceAuthorized     bool                       `json:"evidence_authorized"`
	SourceLicenseExpiresAt int64                      `json:"source_license_expires_at"`
	Name                   *string                    `json:"name"`
	Description            *string                    `json:"description"`
	Rationale              *string                    `json:"rationale"`
	TargetGoal             *string                    `json:"target_goal"`
	Applicability          *string                    `json:"applicability"`
	BaselineSamples        *[]candidateBaselineSample `json:"baseline_samples"`
}

type candidateState struct {
	Row     governedCandidate
	Content reviewCandidate
	Review  runReview
	Run     model.Run
	Bot     model.Bot
	Viewer  string
}

func candidateContentDigest(c *reviewCandidate) string {
	content := map[string]any{
		"skill_id": c.SkillID, "name": c.Name, "description": c.Description,
		"instructions": c.Instructions, "resources": c.Resources, "scripts": c.Scripts,
		"image_digest": c.ImageDigest, "entry_point": c.EntryPoint,
		"publisher_id": c.PublisherID, "scope_key": c.ScopeKey, "source_run_id": c.SourceRunID,
		"baseline_version_id": c.BaselineVersionID, "baseline_instructions": c.BaselineInstructions,
		"target_goal": c.TargetGoal, "applicability": c.Applicability, "rationale": c.Rationale,
		"task": c.RepresentativeTask, "expected": c.ExpectedOutput,
		"input_b64": c.InputBase64, "expected_output_b64": c.ExpectedOutputBase64,
		"evidence_digest": c.EvidenceDigest, "evidence_revision": c.EvidenceRevision, "expires_at": c.ExpiresAt,
		"evidence_id": c.EvidenceID, "evidence_license_expires_at": c.EvidenceLicenseExpiresAt,
	}
	if len(c.BaselineSamples) > 0 {
		content["baseline_samples"] = c.BaselineSamples
	}
	return mustReviewDigest(content)
}
func candidateScriptDigest(c *reviewCandidate) string {
	return mustReviewDigest(skillvalidation.Identity{Scripts: c.Scripts, ImageDigest: c.ImageDigest, EntryPoint: c.EntryPoint})
}
func candidateIdentity(c *reviewCandidate) skillvalidation.Identity {
	return skillvalidation.Identity{Scripts: c.Scripts, ImageDigest: c.ImageDigest, EntryPoint: c.EntryPoint}
}

// CandidateOperation owns source-scoped governance; no Bot-owner shortcut can
// substitute for the current caller's source-material permission.
func (s *Service) CandidateOperation(ctx context.Context, caller, botID, operation string, body []byte) ([]byte, error) {
	if identity.Validate(caller) != nil {
		return nil, status.Error(codes.Unauthenticated, "authenticated requester required")
	}
	if identity.Validate(botID) != nil || len(body) > 1<<20 {
		return nil, status.Error(codes.InvalidArgument, "invalid candidate request")
	}
	if operation == "candidate-list" {
		return s.listGovernedCandidates(ctx, caller, botID)
	}
	var req candidateRequest
	if json.Unmarshal(body, &req) != nil || identity.Validate(req.CandidateID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid candidate_id required")
	}
	if operation != "candidate-get" && (req.SubmissionKey == "" || len(req.SubmissionKey) > 256 || req.Digest == "") {
		return nil, status.Error(codes.InvalidArgument, "submission_key and expected digest required")
	}
	if len(req.Reason) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "reason too long")
	}
	// All governance calls have a separate finite budget and create no Runs,
	// tools, trajectory entries, review jobs, or recursive model evaluations.
	work, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if operation == "candidate-evaluate" {
		return s.evaluateGovernedCandidate(work, caller, botID, req, body)
	}
	var response []byte
	err := s.svc.DB.WithContext(work).Transaction(func(tx *gorm.DB) error {
		state, err := s.lockCandidate(work, tx, caller, botID, req.CandidateID)
		if err != nil {
			return err
		}
		c := &state.Content
		if operation == "candidate-get" {
			if !candidateEvidenceCurrent(state) && !c.CanMaintain {
				return status.Error(codes.PermissionDenied, "scope maintainer required for expired evidence metadata")
			}
			response, err = json.Marshal(candidateView(state))
			return err
		}
		if !c.CanMaintain {
			return status.Error(codes.PermissionDenied, "candidate maintenance permission required")
		}
		if operation != "candidate-revise" && operation != "candidate-reject" && operation != "candidate-request-evidence" && !candidateEvidenceCurrent(state) {
			return status.Error(codes.FailedPrecondition, "source evidence expired; obtain fresh authorized samples and revalidate")
		}
		// Authorization precedes replay, so an old response cannot bypass
		if operation == "candidate-publish" && !c.CanPublish {
			return status.Error(codes.PermissionDenied, "current publisher permission required")
		}
		// revocation. A replay never restores stale validation or approval.
		op := operation + ":" + caller + ":" + req.CandidateID
		replay, err := candidateReplay(tx, botID, op, req.SubmissionKey, body)
		if err != nil {
			return err
		}
		if replay != nil {
			if operation == "candidate-publish" {
				response = replay
				return nil
			}
			response, err = json.Marshal(candidateView(state))
			return err
		}
		if state.Row.Digest != req.Digest {
			return status.Error(codes.Aborted, "candidate content changed; reload current digest")
		}
		if c.Status == "published" {
			if operation != "candidate-publish" {
				return status.Error(codes.FailedPrecondition, "published candidate is immutable")
			}
			result, err := s.publishedCandidate(tx, state)
			if err != nil {
				return err
			}
			response, err = json.Marshal(result)
			if err != nil {
				return err
			}
			return recordCandidateOperation(tx, botID, op, req.SubmissionKey, body, response)
		}
		if c.Status == "rejected" {
			return status.Error(codes.FailedPrecondition, "rejected candidate is immutable")
		}
		if operation != "candidate-reject" && operation != "candidate-request-evidence" && operation != "candidate-revise" {
			if err := candidateFresh(tx, state); err != nil {
				return err
			}
		}
		switch operation {
		case "candidate-revise":
			if err := candidateBaseline(tx, state); err != nil {
				return err
			}
			if err := s.reviseCandidate(state, req); err != nil {
				return err
			}
			c.CanViewMaterials = candidateEvidenceCurrent(state)
			c.CanPublish = c.CanMaintain && c.CanViewMaterials && state.Bot.OwnerType == "user" && state.Bot.OwnerID != nil && *state.Bot.OwnerID == caller && caller == c.PublisherID
			c.CanReviewCode = c.CanPublish
		case "candidate-request-evidence", "candidate-reject":
			c.Reason = req.Reason
			c.Status = "awaiting_evidence"
			if operation == "candidate-reject" {
				c.Status = "rejected"
			}
			c.EvidenceRevision++
			c.Digest = candidateContentDigest(c)
			invalidateCandidate(state)
		case "candidate-code-review":
			if !c.CanReviewCode {
				return status.Error(codes.PermissionDenied, "trusted publisher code-review permission required")
			}
			if !req.Confirmed || len(c.Scripts) == 0 {
				return status.Error(codes.InvalidArgument, "explicit confirmed script review required")
			}
			admission, err := skillvalidation.Review(work, candidateIdentity(c))
			if err != nil {
				return status.Error(codes.FailedPrecondition, "trusted execution manager has not admitted this exact script, image and entry point")
			}
			if state.Row.EvaluationDigest != "" && c.CodeReviewIdentity != admission {
				c.EvidenceRevision++
				c.Digest, c.Status = candidateContentDigest(c), "pending_validation"
				invalidateCandidate(state)
			}
			c.CodeReviewDigest, c.CodeReviewIdentity, c.CodeReviewedBy = candidateScriptDigest(c), admission, caller
			if err := candidateFresh(tx, state); err != nil {
				return err
			}
			member, _, err := repo.RunMember(tx, state.Run.ConvID, caller)
			if err != nil {
				return err
			}
			c.CodeReviewedMemberID = member
		case "candidate-approve":
			if err := candidatePassed(tx, state); err != nil {
				return err
			}
			if err := s.candidatePrincipal(work, tx, state, caller, true); err != nil {
				return err
			}
			if err := candidateFresh(tx, state); err != nil {
				return err
			}
			c.Status = "approved"
			state.Row.ApprovedDigest, state.Row.ApprovedEvaluationDigest, state.Row.ApprovedBy = c.Digest, state.Row.EvaluationDigest, &caller
			member, _, err := repo.RunMember(tx, state.Run.ConvID, caller)
			if err != nil {
				return err
			}
			c.ApprovedMemberID = member
		case "candidate-publish":
			if !c.CanPublish {
				return status.Error(codes.PermissionDenied, "current publisher permission required")
			}
			if c.Status != "approved" || state.Row.ApprovedDigest != c.Digest || state.Row.ApprovedEvaluationDigest != state.Row.EvaluationDigest || state.Row.ApprovedBy == nil {
				return status.Error(codes.FailedPrecondition, "exact content and validation require current approval")
			}
			// Approval remains valid only while its own principal still has
			// both maintenance and material access, not merely the publisher.
			if err := s.candidatePrincipal(work, tx, state, *state.Row.ApprovedBy, true); err != nil {
				return err
			}
			member, _, err := repo.RunMember(tx, state.Run.ConvID, *state.Row.ApprovedBy)
			if err != nil || member != c.ApprovedMemberID {
				return status.Error(codes.PermissionDenied, "approver membership changed; fresh approval required")
			}
			if err := candidatePassed(tx, state); err != nil {
				return err
			}
			result, err := s.publishGovernedCandidate(work, tx, state)
			if err != nil {
				return err
			}
			response, err = json.Marshal(result)
			if err != nil {
				return err
			}
		default:
			return status.Error(codes.InvalidArgument, "unknown candidate operation")
		}
		if err := persistCandidate(tx, state); err != nil {
			return err
		}
		if response == nil {
			response, err = json.Marshal(candidateView(state))
			if err != nil {
				return err
			}
		}
		return recordCandidateOperation(tx, botID, op, req.SubmissionKey, body, response)
	})
	return response, err
}

func (s *Service) listGovernedCandidates(ctx context.Context, caller, botID string) ([]byte, error) {
	var rows []struct{ ID string }
	if err := s.svc.DB.WithContext(ctx).Table("bot.skill_candidates").Select("id").Where("bot_id = ? AND source_review_id IS NOT NULL", botID).Order("created_at DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]reviewCandidate, 0, len(rows))
	for _, row := range rows {
		err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			state, err := s.lockCandidate(ctx, tx, caller, botID, row.ID)
			if err != nil {
				return err
			}
			if state.Content.CanMaintain {
				result = append(result, candidateView(state))
			}
			return nil
		})
		if err != nil && status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.NotFound && status.Code(err) != codes.FailedPrecondition {
			return nil, err
		}
	}
	return json.Marshal(result)
}

func (s *Service) lockCandidate(ctx context.Context, tx *gorm.DB, caller, botID, id string) (*candidateState, error) {
	state := &candidateState{Viewer: caller}
	// Read only enough to obtain the source conversation, then follow the same
	// lock order as Run authority/revocation before locking the mutable candidate.
	var locator struct{ ConvID string }
	err := tx.Table("bot.skill_candidates c").Select("r.conv_id").Joins("JOIN bot.run_reviews r ON r.id = c.source_review_id").Where("c.id = ? AND c.bot_id = ?", id, botID).Take(&locator).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.Error(codes.NotFound, "source candidate not found")
	}
	if err != nil {
		return nil, err
	}
	if err := repo.LockRunConversation(tx, locator.ConvID); err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND bot_id = ?", id, botID).Take(&state.Row).Error; err != nil {
		return nil, err
	}
	if state.Row.SourceReviewID == nil {
		return nil, status.Error(codes.NotFound, "source candidate not found")
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", *state.Row.SourceReviewID).Take(&state.Review).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", state.Review.RunID).Take(&state.Run).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", botID).Take(&state.Bot).Error; err != nil {
		return nil, err
	}
	if err := json.Unmarshal(state.Row.ReviewContent, &state.Content); err != nil {
		return nil, err
	}
	c := &state.Content
	baselineMatches := c.BaselineVersionID == nil && state.Row.BaselineVersionID == nil || c.BaselineVersionID != nil && state.Row.BaselineVersionID != nil && *c.BaselineVersionID == *state.Row.BaselineVersionID
	if !baselineMatches || state.Run.BotID != botID || state.Review.BotID != botID || state.Run.BindingID != state.Review.BindingID || state.Run.ConvID != state.Review.ConvID || c.ID != id || c.SkillID != state.Row.SkillID || c.SourceRunID != state.Run.ID || c.ScopeKey != state.Review.ScopeKey || state.Review.PublisherID == nil || c.PublisherID != *state.Review.PublisherID || c.Digest != state.Row.Digest || c.Instructions != state.Row.Instructions || (c.EvidenceID == "" && c.EvidenceDigest != state.Review.EvidenceDigest) || state.Review.CandidateID == nil || *state.Review.CandidateID != id || (state.Review.Status != "candidate_generated" && state.Review.Status != "expired") {
		return nil, status.Error(codes.FailedPrecondition, "candidate content identity is inconsistent")
	}
	c.Status, c.PublishedVersionID = state.Row.Status, state.Row.PublishedVersionID
	c.RepresentativeTask, c.ExpectedOutput = state.Row.Task, state.Row.ExpectedOutput
	if err := s.candidatePrincipal(ctx, tx, state, caller, false); err != nil {
		return nil, err
	}
	c.CanMaintain = candidateMaintainer(tx, state, caller) == nil
	if !candidateEvidenceCurrent(state) {
		return state, nil
	}
	// This display-only projection never contributes to candidate identity and
	// is reconstructed only after the caller's live material authorization.
	c.BaselineContent = nil
	if c.BaselineVersionID != nil {
		baseline, err := reviewTarget(ctx, tx, &state.Run, &state.Review, *c.BaselineVersionID)
		if err != nil {
			return nil, err
		}
		if baseline.SkillID != c.SkillID {
			return nil, status.Error(codes.FailedPrecondition, "candidate baseline belongs to another skill")
		}
		var version struct{ ValidationContract json.RawMessage }
		if err := tx.Table("bot.skill_versions").Select("validation_contract").Where("id = ? AND skill_id = ?", baseline.ID, c.SkillID).Take(&version).Error; err != nil {
			return nil, err
		}
		var contract reviewCandidate
		if err := json.Unmarshal(version.ValidationContract, &contract); err != nil {
			return nil, status.Error(codes.FailedPrecondition, "baseline execution contract is unavailable")
		}
		// Legacy instruction contracts omit script fields. The actual immutable
		// resource set still comes from the version, not the mutable candidate.
		scripts := contract.Scripts
		if scripts == nil {
			scripts = map[string]string{}
		}
		c.BaselineContent = &candidateBaselineContent{Instructions: baseline.Instructions, Resources: baseline.Resources, Scripts: scripts, ImageDigest: contract.ImageDigest, EntryPoint: contract.EntryPoint}
	}
	c.CanViewMaterials = true
	c.CanMaintain = candidateMaintainer(tx, state, caller) == nil
	c.CanPublish = c.CanMaintain && state.Bot.OwnerType == "user" && state.Bot.OwnerID != nil && *state.Bot.OwnerID == caller && caller == c.PublisherID
	c.CanReviewCode = c.CanPublish
	if c.Status == "approved" && state.Row.ApprovedBy != nil {
		member, _, memberErr := repo.RunMember(tx, state.Run.ConvID, *state.Row.ApprovedBy)
		approvalErr := s.candidatePrincipal(ctx, tx, state, *state.Row.ApprovedBy, true)
		if approvalErr != nil && status.Code(approvalErr) != codes.PermissionDenied && status.Code(approvalErr) != codes.NotFound {
			return nil, approvalErr
		}
		if memberErr != nil || member != c.ApprovedMemberID || approvalErr != nil {
			c.Status, c.ApprovedMemberID, c.Reason = "evaluated", "", "approval authority changed; fresh approval required"
			state.Row.ApprovedDigest, state.Row.ApprovedEvaluationDigest, state.Row.ApprovedBy = "", "", nil
			if err := persistCandidate(tx, state); err != nil {
				return nil, err
			}
		}
	}
	if c.EvidenceID == "" && state.Review.ExpiresAt.After(time.Now()) {
		c.Evidence = string(state.Review.Evidence)
	}
	var validation candidateValidation
	err = tx.Where("candidate_id = ? AND digest = ?", id, c.Digest).Take(&validation).Error
	if err == nil && validation.Status != "running" {
		var result candidateEvaluation
		if err := json.Unmarshal(validation.Result, &result); err != nil {
			return nil, err
		}
		c.Evaluation = &result
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return state, nil
}

func candidateMaintainer(tx *gorm.DB, state *candidateState, user string) error {
	_, role, err := repo.RunMember(tx, state.Run.ConvID, user)
	if err != nil {
		return err
	}
	if state.Run.Visibility == "personal" {
		if state.Review.PersonalUserID != nil && *state.Review.PersonalUserID == user {
			return nil
		}
	} else if role <= 2 {
		return nil
	}
	return status.Error(codes.PermissionDenied, "source scope maintainer permission required")
}

func (s *Service) candidatePrincipal(ctx context.Context, tx *gorm.DB, state *candidateState, user string, maintain bool) error {
	if identity.Validate(user) != nil {
		return status.Error(codes.PermissionDenied, "candidate principal unavailable")
	}
	if maintain {
		if err := candidateMaintainer(tx, state, user); err != nil {
			return err
		}
	}
	if state.Run.AccessRevoked || state.Run.ResultInvalidated || state.Bot.Status != "active" {
		return status.Error(codes.PermissionDenied, "source authority revoked")
	}
	if state.Bot.OwnerID == nil || *state.Bot.OwnerID != state.Content.PublisherID {
		return status.Error(codes.PermissionDenied, "source publisher changed")
	}
	if state.Content.EvidenceID != "" || !candidateEvidenceCurrent(state) {
		return s.candidateRetainedAuthority(ctx, tx, &state.Run, user, make(map[string]bool))
	}
	if err := repo.LockRunAuthority(tx, &state.Run); err != nil {
		return err
	}
	if err := s.candidateMaterialAccess(ctx, tx, &state.Run, user, make(map[string]bool)); err != nil {
		return err
	}
	return s.candidateFrozenSources(ctx, tx, &state.Run, user, make(map[string]bool))
}

// Check each viewer's actual source permissions recursively. Keeping the
// original requester's grants alone would let a Bot owner read private inputs.
func (s *Service) candidateMaterialAccess(ctx context.Context, tx *gorm.DB, r *model.Run, user string, visited map[string]bool) error {
	if visited[r.ID] {
		return status.Error(codes.PermissionDenied, "cyclic source ancestry")
	}
	visited[r.ID] = true
	defer delete(visited, r.ID)
	if r.Visibility == "personal" && (r.PersonalUserID == nil || *r.PersonalUserID != user) {
		return status.Error(codes.PermissionDenied, "personal source materials are private")
	}
	if _, _, err := repo.RunMember(tx, r.ConvID, user); err != nil {
		return err
	}
	access := resourceAccess(r)
	access.RequesterID = user
	if err := resourcegrant.CheckIDs(tx, access, r.Visibility != "personal", true); err != nil {
		return err
	}
	var materials []Material
	if err := json.Unmarshal(r.Materials, &materials); err != nil {
		return err
	}
	for _, material := range materials {
		if material.FileID != "" {
			if _, err := resourcegrant.Check(tx, access, "file", material.FileID, r.Visibility != "personal", true); err != nil {
				return err
			}
			if s.files == nil {
				return status.Error(codes.Unavailable, "file permission service unavailable")
			}
			// This call verifies the human viewer, not a delegated Bot identity.
			if _, err := s.files.GetFileInfo(userContext(ctx, user), &filepb.GetFileInfoReq{FileId: material.FileID, UserId: user}); err != nil {
				return err
			}
		}
		if material.KnowledgeID != "" {
			if err := s.candidateKnowledgeAccess(ctx, tx, access, user, material.KnowledgeID); err != nil {
				return err
			}
		}
		if material.HistoryGrantID != "" {
			source := resourcegrant.HistorySource{MessageID: material.MessageID, AcceptedEditCount: material.EditCount, HistoryGrantID: material.HistoryGrantID, HistorySeq: material.Seq}
			if err := resourcegrant.CheckHistorySource(tx, access, source, true); err != nil {
				return err
			}
		}
		if material.ParentRunID != "" {
			var parent model.Run
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", material.ParentRunID).Take(&parent).Error; err != nil {
				return err
			}
			if err := s.candidateMaterialAccess(ctx, tx, &parent, user, visited); err != nil {
				return err
			}
		}
	}
	var provenance []model.SourceVersion
	if len(r.Provenance) > 0 {
		if err := json.Unmarshal(r.Provenance, &provenance); err != nil {
			return err
		}
	}
	for _, source := range provenance {
		if err := resourcegrant.CheckHistorySource(tx, access, source.HistorySource(), true); err != nil {
			return err
		}
		if source.KnowledgeID != "" {
			if err := s.candidateKnowledgeAccess(ctx, tx, access, user, source.KnowledgeID); err != nil {
				return err
			}
		}
		if source.ParentRunID != "" {
			var parent model.Run
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", source.ParentRunID).Take(&parent).Error; err != nil {
				return err
			}
			if err := s.candidateMaterialAccess(ctx, tx, &parent, user, visited); err != nil {
				return err
			}
		}
	}
	return repo.CheckRunSources(tx, r)
}

func (s *Service) candidateKnowledgeAccess(ctx context.Context, tx *gorm.DB, access resourcegrant.Access, user, id string) error {
	if _, err := resourcegrant.Check(tx, access, "knowledge", id, access.Visibility != "personal", true); err != nil {
		return err
	}
	if s.kb == nil {
		return status.Error(codes.Unavailable, "knowledge permission service unavailable")
	}
	_, err := s.kb.GetKB(userContext(ctx, user), &kb.GetKBReq{KbId: id})
	return err
}

func candidateFresh(tx *gorm.DB, state *candidateState) error {
	if !candidateEvidenceCurrent(state) {
		return status.Error(codes.FailedPrecondition, "source evidence expired; obtain fresh evidence")
	}
	return candidateBaseline(tx, state)
}

func candidateBaseline(tx *gorm.DB, state *candidateState) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-publish:"+state.Row.SkillID).Error; err != nil {
		return err
	}
	var latest struct {
		ID       string
		Status   string
		ScopeKey string
	}
	err := tx.Table("bot.skill_versions").Select("id,status,scope_key").Where("skill_id = ? AND scope_key IN ?", state.Row.SkillID, []string{"bot", state.Content.ScopeKey}).Order("version DESC").Take(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if (err == nil && (state.Row.BaselineVersionID == nil || latest.ID != *state.Row.BaselineVersionID)) || (errors.Is(err, gorm.ErrRecordNotFound) && state.Row.BaselineVersionID != nil) {
		return status.Error(codes.Aborted, "candidate baseline is stale; re-integrate before validation or publication")
	}
	if state.Row.BaselineVersionID != nil {
		if latest.Status != "published" {
			return status.Error(codes.PermissionDenied, "baseline version was disabled")
		}
		var count int64
		if err := tx.Table("bot.skill_bindings").Where("bot_id = ? AND skill_id = ? AND scope_key = ? AND enabled = true", state.Row.BotID, state.Row.SkillID, latest.ScopeKey).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return status.Error(codes.PermissionDenied, "baseline skill enablement revoked")
		}
	}
	return nil
}

func invalidateCandidate(state *candidateState) {
	state.Content.Evaluation = nil
	state.Row.EvaluationDigest, state.Row.ApprovedDigest, state.Row.ApprovedEvaluationDigest, state.Row.ApprovedBy = "", "", "", nil
}

func (s *Service) reviseCandidate(state *candidateState, req candidateRequest) error {
	c := &state.Content
	oldDigest, oldScripts := c.Digest, candidateScriptDigest(c)
	renew := req.EvidenceAuthorized
	if !candidateEvidenceCurrent(state) && !renew {
		return status.Error(codes.FailedPrecondition, "expired evidence requires newly authorized samples, expected results and license")
	}
	if req.SourceLicenseExpiresAt != 0 && !renew {
		return status.Error(codes.InvalidArgument, "explicit new evidence authorization required")
	}
	if renew && (req.Name == nil || req.Description == nil || req.Rationale == nil || req.Instructions == nil || req.Resources == nil || req.Scripts == nil || req.ImageDigest == nil || req.EntryPoint == nil) {
		return status.Error(codes.InvalidArgument, "new evidence requires an explicitly resubmitted complete authorized package")
	}
	if !candidateEvidenceCurrent(state) {
		*c = candidateView(state)
		c.TargetGoal, c.Applicability, c.Reason = "", "", ""
	}
	if renew {
		c.TargetGoal, c.Applicability, c.Reason, c.BaselineContent = "", "", "", nil
		if req.Task == nil || req.ExpectedOutput == nil || strings.TrimSpace(*req.Task) == "" || strings.TrimSpace(*req.ExpectedOutput) == "" || req.SourceLicenseExpiresAt <= time.Now().Unix() {
			return status.Error(codes.InvalidArgument, "new authorized task, independent expected output and future source license deadline required")
		}
		deadline, err := s.reviewRetentionDeadline(time.Now())
		if err != nil {
			return err
		}
		c.ExpiresAt = min(deadline.Unix(), req.SourceLicenseExpiresAt)
		if req.ExpiresAt != nil && (*req.ExpiresAt <= time.Now().Unix() || *req.ExpiresAt > c.ExpiresAt) {
			return status.Error(codes.InvalidArgument, "evidence deadline exceeds configured retention or source license")
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		c.EvidenceID, c.EvidenceLicenseExpiresAt = id, req.SourceLicenseExpiresAt
		c.InputBase64, c.ExpectedOutputBase64, c.Evidence, c.BaselineInstructions = "", "", "", ""
		c.CodeReviewDigest, c.CodeReviewIdentity, c.CodeReviewedBy, c.CodeReviewedMemberID, c.ApprovedMemberID = "", "", "", "", ""
		c.BaselineSamples = nil
	}
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.Description != nil {
		c.Description = *req.Description
	}
	if req.Rationale != nil {
		c.Rationale = *req.Rationale
	}
	if req.TargetGoal != nil {
		c.TargetGoal = *req.TargetGoal
	}
	if req.Applicability != nil {
		c.Applicability = *req.Applicability
	}
	if req.Instructions != nil {
		c.Instructions = *req.Instructions
	}
	if req.Resources != nil {
		c.Resources = *req.Resources
	}
	if req.Scripts != nil {
		c.Scripts = *req.Scripts
	}
	if req.ImageDigest != nil {
		c.ImageDigest = *req.ImageDigest
	}
	if req.EntryPoint != nil {
		c.EntryPoint = *req.EntryPoint
	}
	if req.Task != nil {
		c.RepresentativeTask = *req.Task
	}
	if req.ExpectedOutput != nil {
		c.ExpectedOutput = *req.ExpectedOutput
	}
	if req.InputBase64 != nil {
		c.InputBase64 = *req.InputBase64
	}
	if req.ExpectedOutputBase64 != nil {
		c.ExpectedOutputBase64 = *req.ExpectedOutputBase64
	}
	if req.ExpiresAt != nil {
		if *req.ExpiresAt <= time.Now().Unix() || *req.ExpiresAt > c.ExpiresAt {
			return status.Error(codes.InvalidArgument, "sample expiry may only shorten the current evidence deadline")
		}
		c.ExpiresAt = *req.ExpiresAt
	}
	value := reviewExtraction{Name: c.Name, Description: c.Description, Instructions: c.Instructions, Resources: c.Resources, Scripts: c.Scripts, ImageDigest: c.ImageDigest, EntryPoint: c.EntryPoint, TargetGoal: c.TargetGoal, Applicability: c.Applicability, Rationale: c.Rationale, RepresentativeTask: c.RepresentativeTask, ExpectedOutput: c.ExpectedOutput}
	if req.BaselineSamples != nil {
		if !renew || len(*req.BaselineSamples) > 7 {
			return status.Error(codes.InvalidArgument, "bounded baseline samples require explicit new evidence authorization")
		}
		seen := make(map[string]bool)
		for _, sample := range *req.BaselineSamples {
			if identity.Validate(sample.VersionID) != nil || seen[sample.VersionID] || len(sample.Task) > 16384 || len(sample.Expected) > 2048 || strings.TrimSpace(sample.Task) == "" || strings.TrimSpace(sample.Expected) == "" {
				return status.Error(codes.InvalidArgument, "baseline samples require unique version identities and bounded tasks with exact expected results")
			}
			seen[sample.VersionID] = true
			for _, encoded := range []string{sample.InputBase64, sample.ExpectedOutputBase64} {
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil || len(decoded) > 65536 {
					return status.Error(codes.InvalidArgument, "invalid bounded baseline artifact")
				}
			}
		}
		c.BaselineSamples = *req.BaselineSamples
	}
	if err := validateReviewExtraction(&value, reviewAssessment{TargetGoal: c.TargetGoal, Applicability: c.Applicability}); err != nil {
		return status.Error(codes.InvalidArgument, "revised candidate exceeds content bounds")
	}
	c.Resources, c.Scripts = value.Resources, value.Scripts
	for _, files := range []map[string]string{c.Resources, c.Scripts} {
		for _, value := range files {
			if len(value) > 65536 {
				return status.Error(codes.InvalidArgument, "individual candidate file exceeds 64 KiB")
			}
		}
	}
	for _, encoded := range []string{c.InputBase64, c.ExpectedOutputBase64} {
		if len(encoded) > base64.StdEncoding.EncodedLen(65536) {
			return status.Error(codes.InvalidArgument, "sample exceeds validation budget")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) > 65536 {
			return status.Error(codes.InvalidArgument, "invalid bounded base64 sample")
		}
	}
	if renew {
		if len(c.Scripts) > 0 && (req.InputBase64 == nil || req.ExpectedOutputBase64 == nil || c.InputBase64 == "" || c.ExpectedOutputBase64 == "") {
			return status.Error(codes.InvalidArgument, "new script evidence requires explicit input and expected output artifacts")
		}
		c.EvidenceDigest = mustReviewDigest(map[string]any{"id": c.EvidenceID, "task": c.RepresentativeTask, "expected": c.ExpectedOutput, "input_b64": c.InputBase64, "expected_output_b64": c.ExpectedOutputBase64, "baseline_samples": c.BaselineSamples, "license_expires_at": c.EvidenceLicenseExpiresAt})
	}
	c.CodeReviewRequired = len(c.Scripts) > 0
	if oldScripts != candidateScriptDigest(c) {
		c.CodeReviewDigest, c.CodeReviewIdentity, c.CodeReviewedBy, c.CodeReviewedMemberID = "", "", "", ""
	}
	c.Digest = candidateContentDigest(c)
	if oldDigest != c.Digest {
		c.EvidenceRevision++
		c.Digest = candidateContentDigest(c)
		c.Status, c.Reason = "pending_validation", ""
		invalidateCandidate(state)
	}
	if c.Status == "awaiting_evidence" && oldDigest == c.Digest {
		return status.Error(codes.FailedPrecondition, "new evidence is required before revalidation")
	}
	return nil
}

func persistCandidate(tx *gorm.DB, state *candidateState) error {
	// Viewer capabilities/evidence are never a stored authorization decision.
	stored := state.Content
	stored.CanMaintain, stored.CanViewMaterials, stored.CanReviewCode, stored.CanPublish, stored.Evidence = false, false, false, false, ""
	stored.Evaluation = nil
	stored.BaselineContent = nil
	raw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	resources, err := json.Marshal(stored.Resources)
	if err != nil {
		return err
	}
	if stored.EvidenceID == "" && stored.ExpiresAt < state.Review.ExpiresAt.Unix() {
		deadline := time.Unix(stored.ExpiresAt, 0)
		if err := tx.Model(&runReview{}).Where("id = ? AND expires_at > ?", state.Review.ID, deadline).Update("expires_at", deadline).Error; err != nil {
			return err
		}
		state.Review.ExpiresAt = deadline
	}
	if stored.EvidenceID != "" {
		if err := tx.Model(&candidateValidation{}).Where("candidate_id = ? AND digest <> ? AND jsonb_exists(result, 'reason')", stored.ID, stored.Digest).Update("result", gorm.Expr("result - 'reason'")).Error; err != nil {
			return err
		}
		if err := tx.Table("bot.skill_evaluations").Where("candidate_id = ? AND (reason <> '' OR observed <> '')", stored.ID).Updates(map[string]any{"reason": "", "observed": ""}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&governedCandidate{}).Where("id = ?", state.Row.ID).Updates(map[string]any{
		"review_content": json.RawMessage(raw), "digest": stored.Digest, "status": stored.Status,
		"instructions": stored.Instructions, "resources": json.RawMessage(resources), "task": stored.RepresentativeTask,
		"expected_output": stored.ExpectedOutput, "evaluation_digest": state.Row.EvaluationDigest,
		"approved_digest": state.Row.ApprovedDigest, "approved_evaluation_digest": state.Row.ApprovedEvaluationDigest,
		"approved_by": state.Row.ApprovedBy, "published_version_id": stored.PublishedVersionID, "updated_at": time.Now(),
	}).Error
}

func candidateReplay(tx *gorm.DB, botID, operation, key string, body []byte) ([]byte, error) {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "candidate-operation:"+botID+":"+operation+":"+key).Error; err != nil {
		return nil, err
	}
	var prior struct {
		RequestHash string
		Response    json.RawMessage
	}
	err := tx.Table("bot.skill_operations").Where("bot_id = ? AND operation = ? AND submission_key = ?", botID, operation, key).Take(&prior).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if prior.RequestHash != mustReviewDigest(json.RawMessage(body)) {
		return nil, status.Error(codes.AlreadyExists, "submission key has different content")
	}
	return prior.Response, nil
}
func recordCandidateOperation(tx *gorm.DB, botID, operation, key string, body, response []byte) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	if strings.HasPrefix(operation, "candidate-") && !strings.HasPrefix(operation, "candidate-publish:") && !strings.HasPrefix(operation, "candidate-evaluate:") {
		var content struct {
			ID               string `json:"id"`
			Digest           string `json:"digest"`
			Status           string `json:"status"`
			EvidenceID       string `json:"evidence_id,omitempty"`
			EvidenceRevision int64  `json:"evidence_revision"`
		}
		if err := json.Unmarshal(response, &content); err != nil {
			return err
		}
		response, err = json.Marshal(content)
		if err != nil {
			return err
		}
	}
	return tx.Table("bot.skill_operations").Create(map[string]any{"id": id, "bot_id": botID, "operation": operation, "submission_key": key, "request_hash": mustReviewDigest(json.RawMessage(body)), "response": json.RawMessage(response), "created_at": time.Now()}).Error
}

func candidatePassed(tx *gorm.DB, state *candidateState) error {
	c := &state.Content
	if c.Status == "awaiting_evidence" || c.Status == "rejected" || c.Digest != candidateContentDigest(c) {
		return status.Error(codes.FailedPrecondition, "candidate content or evidence is not valid")
	}
	var validation candidateValidation
	if err := tx.Where("candidate_id = ? AND digest = ? AND status = 'completed'", c.ID, c.Digest).Take(&validation).Error; err != nil {
		return status.Error(codes.FailedPrecondition, "candidate has no completed verification")
	}
	var result candidateEvaluation
	if err := json.Unmarshal(validation.Result, &result); err != nil {
		return err
	}
	if result.Status != "passed" || !result.Passed || result.Digest != c.Digest || result.EvidenceDigest != c.EvidenceDigest || state.Row.EvaluationDigest != mustReviewDigest(result) {
		return status.Error(codes.FailedPrecondition, "candidate requires passed exact-content validation")
	}
	if len(c.Scripts) > 0 && (c.CodeReviewIdentity == "" || c.CodeReviewDigest != candidateScriptDigest(c) || result.CodeReviewIdentity != c.CodeReviewIdentity) {
		return status.Error(codes.FailedPrecondition, "script identity requires renewed trusted review and validation")
	}
	c.Evaluation = &result
	return nil
}

func (s *Service) publishedCandidate(tx *gorm.DB, state *candidateState) (*skill.VersionView, error) {
	if state.Row.PublishedVersionID == nil {
		return nil, status.Error(codes.FailedPrecondition, "published version missing")
	}
	var row struct {
		ID          string
		Version     int
		Name        string
		Description string
		Digest      string
		Status      string
		CreatedAt   time.Time
	}
	if err := tx.Table("bot.skill_versions").Where("id = ? AND skill_id = ?", *state.Row.PublishedVersionID, state.Row.SkillID).Take(&row).Error; err != nil {
		return nil, err
	}
	enabled, err := skill.VersionEnabled(tx, state.Row.BotID, row.ID)
	return &skill.VersionView{VersionID: row.ID, Version: row.Version, Name: row.Name, Description: row.Description, Digest: row.Digest, Status: row.Status, Enabled: enabled, CreatedAt: row.CreatedAt.Unix()}, err
}

func (s *Service) publishGovernedCandidate(ctx context.Context, tx *gorm.DB, state *candidateState) (*skill.VersionView, error) {
	c := &state.Content
	if len(c.Scripts) > 0 {
		admission, err := skillvalidation.Review(ctx, candidateIdentity(c))
		if err != nil || admission != c.CodeReviewIdentity {
			return nil, status.Error(codes.FailedPrecondition, "trusted execution admission changed")
		}
		if err := s.candidatePrincipal(ctx, tx, state, c.CodeReviewedBy, true); err != nil {
			return nil, err
		}
		member, _, err := repo.RunMember(tx, state.Run.ConvID, c.CodeReviewedBy)
		if err != nil || member != c.CodeReviewedMemberID {
			return nil, status.Error(codes.PermissionDenied, "code reviewer membership changed; fresh review required")
		}
	}
	if err := candidateFresh(tx, state); err != nil {
		return nil, err
	}
	if err := s.candidatePrincipal(ctx, tx, state, c.PublisherID, true); err != nil {
		return nil, err
	}
	if state.Row.ApprovedBy == nil {
		return nil, status.Error(codes.FailedPrecondition, "approval principal unavailable")
	}
	if err := s.candidatePrincipal(ctx, tx, state, *state.Row.ApprovedBy, true); err != nil {
		return nil, err
	}
	// Never bind a source-derived version into the Bot-global catalog. Cross-
	// scope reuse requires a separate authorization workflow, not publication.
	var pkg struct {
		ScopeKey    string
		PublisherID string
	}
	if err := tx.Table("bot.skill_packages").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ?", c.SkillID, state.Row.BotID).Take(&pkg).Error; err != nil {
		return nil, err
	}
	if (pkg.ScopeKey != c.ScopeKey && pkg.ScopeKey != "bot") || pkg.PublisherID != c.PublisherID {
		return nil, status.Error(codes.PermissionDenied, "publication scope is not the authorized source scope")
	}
	var number int
	if err := tx.Table("bot.skill_versions").Select("COALESCE(MAX(version),0)").Where("skill_id = ?", c.SkillID).Scan(&number).Error; err != nil {
		return nil, err
	}
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	resources, err := json.Marshal(c.Resources)
	if err != nil {
		return nil, err
	}
	contract, err := json.Marshal(candidatePublishedContract(c))
	if err != nil {
		return nil, err
	}
	contract, err = skill.StampContract(contract, map[string]any{"scripts": len(c.Scripts) > 0}, map[string]any{"scope": c.ScopeKey, "source_authority_digest": mustReviewDigest(map[string]any{"materials": json.RawMessage(state.Run.Materials), "provenance": json.RawMessage(state.Run.Provenance)})})
	if err != nil {
		return nil, err
	}
	if err := candidateFresh(tx, state); err != nil {
		return nil, err
	}
	now := time.Now()
	if err := tx.Table("bot.skill_versions").Create(map[string]any{"id": id, "skill_id": c.SkillID, "bot_id": state.Row.BotID, "scope_key": c.ScopeKey, "version": number + 1, "digest": c.Digest, "name": c.Name, "description": c.Description, "instructions": c.Instructions, "resources": json.RawMessage(resources), "license": "private-source-scope", "status": "published", "validation_contract": json.RawMessage(contract), "created_at": now}).Error; err != nil {
		return nil, err
	}
	enabled, err := skill.AdoptPublished(ctx, tx, state.Row.BotID, c.SkillID, id, c.ScopeKey, c.PublisherID)
	if err != nil {
		return nil, err
	}
	c.Status, c.PublishedVersionID = "published", &id
	return &skill.VersionView{VersionID: id, Version: number + 1, Name: c.Name, Description: c.Description, Digest: c.Digest, Status: "published", Enabled: enabled, CreatedAt: now.Unix()}, nil
}
