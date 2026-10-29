package run

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func candidateEvidenceCurrent(state *candidateState) bool {
	c := &state.Content
	now := time.Now()
	if c.ExpiresAt <= now.Unix() || c.Status == "expired" {
		return false
	}
	if c.EvidenceID != "" {
		return c.EvidenceLicenseExpiresAt > now.Unix() && c.ExpiresAt <= c.EvidenceLicenseExpiresAt
	}
	return state.Review.ExpiresAt.After(now) && c.ExpiresAt <= state.Review.ExpiresAt.Unix()
}

// Expired evidence has an allowlisted maintenance projection, never a partially
// redacted content object. Digest remains the optimistic concurrency identity.
func candidateView(state *candidateState) reviewCandidate {
	c := state.Content
	if candidateEvidenceCurrent(state) {
		return c
	}
	statusValue := "expired"
	if state.Row.Status == "published" || state.Row.Status == "rejected" {
		statusValue = state.Row.Status
	}
	return reviewCandidate{ID: c.ID, Origin: c.Origin, PackageDigest: c.PackageDigest, PublisherAuthorizationVersion: c.PublisherAuthorizationVersion, ParentCandidateID: c.ParentCandidateID, SourceReviewID: c.SourceReviewID, SkillID: c.SkillID, SourceRunID: c.SourceRunID, PublisherID: c.PublisherID, ScopeKey: c.ScopeKey, Intent: c.Intent, Digest: c.Digest, BaselineVersionID: c.BaselineVersionID, PublishedVersionID: c.PublishedVersionID, Status: statusValue, CodeReviewRequired: c.CodeReviewRequired, EvidenceID: c.EvidenceID, EvidenceDigest: c.EvidenceDigest, EvidenceLicenseExpiresAt: c.EvidenceLicenseExpiresAt, EvidenceRevision: c.EvidenceRevision, ExpiresAt: c.ExpiresAt, CanMaintain: c.CanMaintain, Reason: "evidence expired; submit newly authorized package, samples and independent expected results, then validate and approve again"}
}

// Immutable publication retains execution identity and non-sensitive conclusions,
// not representative business inputs, expected results, evidence or free text.
func candidatePublishedContract(c *reviewCandidate) map[string]any {
	var conclusion map[string]any
	if c.Evaluation != nil {
		e := c.Evaluation
		conclusion = map[string]any{"status": e.Status, "passed": e.Passed, "digest": e.Digest, "task_digest": e.TaskDigest, "expected_digest": e.ExpectedDigest, "output_digest": e.OutputDigest, "model_version": e.ModelVersion, "environment_digest": e.EnvironmentDigest, "baseline_digest": e.BaselineDigest, "evidence_digest": e.EvidenceDigest, "execution_id": e.ExecutionID, "code_review_identity": e.CodeReviewIdentity, "created_at": e.CreatedAt}
	}
	return map[string]any{"id": c.ID, "origin": c.Origin, "package_digest": c.PackageDigest, "digest": c.Digest, "baseline_version_id": c.BaselineVersionID, "scripts": c.Scripts, "image_digest": c.ImageDigest, "entry_point": c.EntryPoint, "code_review_digest": c.CodeReviewDigest, "code_review_identity": c.CodeReviewIdentity, "evaluation": conclusion, "io_contract_digest": mustReviewDigest([]string{c.RepresentativeTask, c.ExpectedOutput, c.InputBase64, c.ExpectedOutputBase64})}
}

func (s *Service) candidateRetainedAuthority(ctx context.Context, tx *gorm.DB, r *model.Run, user string, visited map[string]bool) error {
	if r.Visibility == "personal" && (r.PersonalUserID == nil || *r.PersonalUserID != user) {
		return status.Error(codes.PermissionDenied, "personal source scope is private")
	}
	if _, _, err := repo.RunMember(tx, r.ConvID, user); err != nil {
		return err
	}
	if err := s.candidateSourceRevocation(ctx, tx, r); err != nil {
		return err
	}
	return s.candidateFrozenSources(ctx, tx, r, user, visited)
}

// Expiration ends permission to read old material, but is not an explicit
// revocation of an accepted method or a newly licensed replacement sample.
// Preserve all source revocation fences without reading original message text.
func (s *Service) candidateSourceRevocation(ctx context.Context, tx *gorm.DB, r *model.Run) error {
	return s.candidateSourceRevocationVisit(ctx, tx, r, make(map[string]bool))
}

func (s *Service) candidateSourceRevocationVisit(ctx context.Context, tx *gorm.DB, r *model.Run, visited map[string]bool) error {
	var current model.Run
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", r.ID).Take(&current).Error; err != nil {
		return err
	}
	r = &current
	if visited[r.ID] || len(visited) >= 32 || r.AccessRevoked || r.ResultInvalidated {
		return status.Error(codes.PermissionDenied, "source authority revoked or unavailable")
	}
	visited[r.ID] = true
	defer delete(visited, r.ID)
	// Reuse the existing participation/epoch/controller fence. Resource and
	// provenance checks below deliberately distinguish revocation from old TTLs.
	authority := *r
	authority.ResourceGrantIDs = nil
	authority.Materials, authority.Provenance = json.RawMessage(`[]`), json.RawMessage(`[]`)
	if err := repo.LockRunAuthority(tx, &authority); err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			return status.Error(codes.PermissionDenied, "source participation authority unavailable")
		}
		return err
	}
	for _, id := range r.ResourceGrantIDs {
		found := false
		for _, kind := range []string{"file", "knowledge"} {
			var grant resourcegrant.Grant
			err := tx.Table(resourcegrant.Table(kind)).Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", id).Take(&grant).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			found = true
			if grant.RevokedAt != nil || !grant.CanRead || r.Visibility != "personal" && !grant.CanShare || grant.BotID != r.BotID || grant.ConvID != r.ConvID || grant.BindingID != r.BindingID || grant.BotAuthorizationVersion != r.BotAuthorizationVersion || grant.BindingAuthorizationVersion != r.BindingAuthorizationVersion {
				return status.Error(codes.PermissionDenied, "source resource authority revoked")
			}
			member, _, err := repo.RunMember(tx, r.ConvID, grant.SubjectID)
			if err != nil || member != grant.SubjectMemberID {
				return status.Error(codes.PermissionDenied, "source grant membership changed")
			}
			owner, err := resourcegrant.Owner(tx, kind, grant.ResourceID)
			if err != nil || owner != grant.OwnerID {
				return status.Error(codes.PermissionDenied, "source resource changed")
			}
			if kind == "knowledge" {
				var count int64
				if grant.ResourceBindingID == nil {
					return status.Error(codes.PermissionDenied, "knowledge source binding revoked")
				}
				if err := tx.Table("knowledge.knowledge_bindings").Where("id = ? AND kb_id = ?", *grant.ResourceBindingID, grant.ResourceID).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					return status.Error(codes.PermissionDenied, "knowledge source binding revoked")
				}
			}
			break
		}
		if !found {
			return status.Error(codes.PermissionDenied, "source resource grant unavailable")
		}
	}
	var materials []Material
	if err := json.Unmarshal(r.Materials, &materials); err != nil {
		return err
	}
	var sources []model.SourceVersion
	if len(r.Provenance) > 0 {
		if err := json.Unmarshal(r.Provenance, &sources); err != nil {
			return err
		}
	}
	for _, material := range materials {
		if material.Kind == "conversation_history" {
			if err := s.candidateHistorySource(ctx, tx, r, resourcegrant.HistorySource{MessageID: material.MessageID, AcceptedEditCount: material.EditCount, HistoryGrantID: material.HistoryGrantID, HistorySeq: material.Seq}, visited); err != nil {
				return err
			}
		} else if material.HistoryGrantID != "" {
			if err := repo.CheckMaterialHistory(tx, r, material.HistoryGrantID, material.Seq); err != nil {
				return err
			}
		}
		if material.ParentRunID != "" {
			sources = append(sources, model.SourceVersion{ParentRunID: material.ParentRunID})
		}
	}
	if r.ParentRunID != nil {
		sources = append(sources, model.SourceVersion{ParentRunID: *r.ParentRunID})
	}
	for _, source := range sources {
		if source.HistoryGrantID != "" || source.HistorySeq != 0 {
			if err := s.candidateHistorySource(ctx, tx, r, source.HistorySource(), visited); err != nil {
				return err
			}
		}
		if source.KnowledgeID != "" {
			if err := resourcegrant.CheckKnowledgeSource(tx, source.KnowledgeSource(), true); err != nil {
				return err
			}
		}
		if source.ParentRunID != "" {
			var parent model.Run
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", source.ParentRunID).Take(&parent).Error; err != nil {
				return err
			}
			if err := s.candidateSourceRevocationVisit(ctx, tx, &parent, visited); err != nil {
				return err
			}
		}
	}
	return nil
}

// Retained work ignores personal deletion, not recall or a changed live history
// version. Read only source metadata; never reload the expired message body.
func (s *Service) candidateHistorySource(ctx context.Context, tx *gorm.DB, r *model.Run, source resourcegrant.HistorySource, visited map[string]bool) error {
	if identity.Validate(source.MessageID) != nil || source.HistorySeq < 1 || source.AcceptedEditCount < 0 {
		return status.Error(codes.PermissionDenied, "invalid retained history source")
	}
	grant, err := resourcegrant.CheckHistoryGrant(tx, resourceAccess(r), source.HistoryGrantID, true)
	if err != nil {
		return err
	}
	if source.HistorySeq < grant.StartSeq || source.HistorySeq > grant.EndSeq {
		return status.Error(codes.PermissionDenied, "retained history source is outside its grant")
	}
	var message struct {
		SenderType          string
		SenderID, BindingID *string
		RawPayload          string
	}
	err = tx.Table("messaging.messages").Select("sender_type,sender_id,binding_id,raw_payload").
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id = ? AND conv_id = ? AND seq = ? AND edit_count = ? AND status <> 2", source.MessageID, r.ConvID, source.HistorySeq, source.AcceptedEditCount).Take(&message).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.PermissionDenied, "retained history source was recalled or changed")
	}
	if err != nil {
		return err
	}
	var metadata struct {
		RunID string `json:"run_id"`
	}
	if message.SenderType != "bot" || json.Unmarshal([]byte(message.RawPayload), &metadata) != nil || identity.Validate(metadata.RunID) != nil {
		return nil
	}
	var parent model.Run
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", metadata.RunID).Take(&parent).Error; err != nil {
		return err
	}
	if message.SenderID == nil || *message.SenderID != parent.BotID || message.BindingID == nil || *message.BindingID != parent.BindingID || parent.ConvID != r.ConvID || parent.Visibility != "conversation" {
		return status.Error(codes.PermissionDenied, "retained history result source changed")
	}
	return s.candidateSourceRevocationVisit(ctx, tx, &parent, visited)
}
