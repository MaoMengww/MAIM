package skill

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/skillvalidation"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BlockedUpgrade struct {
	Version VersionView `json:"version"`
	Reasons []string    `json:"reasons"`
}

type VersionActivity struct {
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	ActorID         string `json:"actor_id"`
	VersionID       string `json:"version_id"`
	Version         int    `json:"version"`
	Digest          string `json:"digest"`
	BindingRevision int64  `json:"binding_revision"`
	CreatedAt       int64  `json:"created_at"`
}

type VersionRequest struct {
	VersionID        string `json:"version_id"`
	SubmissionKey    string `json:"submission_key"`
	Digest           string `json:"digest"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

// VersionRecord exposes immutable identity to runtime's source-authority gate.
type VersionRecord struct {
	ID       string
	BotID    string
	SkillID  string
	ScopeKey string
	Digest   string
}

func FindVersion(tx *gorm.DB, botID, id string) (*VersionRecord, error) {
	var row VersionRecord
	if err := tx.Table("bot.skill_versions").Where("id = ? AND bot_id = ?", id, botID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "skill version not found")
		}
		return nil, err
	}
	return &row, nil
}

// StampContract adds only server-observed authority. Model prose and claimed
// compatibility are deliberately excluded from automatic adoption decisions.
func StampContract(raw json.RawMessage, permissions, dataScope any) (json.RawMessage, error) {
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		return nil, err
	}
	contract["governance"] = map[string]any{"permissions": permissions, "data_scope": dataScope}
	return json.Marshal(contract)
}

func contractChanges(before, after *version) []string {
	var old, next map[string]any
	if json.Unmarshal(before.ValidationContract, &old) != nil || json.Unmarshal(after.ValidationContract, &next) != nil {
		return []string{"validation_contract_unavailable"}
	}
	reasons := []string{}
	oldGov, oldOK := old["governance"].(map[string]any)
	nextGov, nextOK := next["governance"].(map[string]any)
	if !oldOK || !nextOK {
		reasons = append(reasons, "trusted_contract_unavailable")
	} else {
		if !reflect.DeepEqual(oldGov["permissions"], nextGov["permissions"]) {
			reasons = append(reasons, "permissions_changed")
		}
		if before.ScopeKey != after.ScopeKey || !reflect.DeepEqual(oldGov["data_scope"], nextGov["data_scope"]) {
			reasons = append(reasons, "data_scope_changed")
		}
	}
	for _, key := range []string{"image_digest", "entry_point"} {
		if !reflect.DeepEqual(old[key], next[key]) {
			reasons = append(reasons, "environment_changed")
			break
		}
	}
	_, oldScripts := old["scripts"].(map[string]any)
	_, newScripts := next["scripts"].(map[string]any)
	if oldScripts != newScripts {
		reasons = append(reasons, "environment_changed")
	}
	for _, key := range []string{"representative_task", "expected_output", "input_b64", "expected_output_b64"} {
		if !reflect.DeepEqual(old[key], next[key]) {
			reasons = append(reasons, "io_contract_changed")
			break
		}
	}
	return reasons
}

// executionAdmission never treats an approved prose claim as code review.
// Script publication must retain completed exact-content validation and the
// currently configured trusted adapter's exact script/image/entry admission.
func executionAdmission(ctx context.Context, tx *gorm.DB, target *version) error {
	var c struct {
		ID                 string            `json:"id"`
		Digest             string            `json:"digest"`
		Scripts            map[string]string `json:"scripts"`
		ImageDigest        string            `json:"image_digest"`
		EntryPoint         string            `json:"entry_point"`
		CodeReviewDigest   string            `json:"code_review_digest"`
		CodeReviewIdentity string            `json:"code_review_identity"`
		Evaluation         struct {
			Status             string `json:"status"`
			Passed             bool   `json:"passed"`
			Digest             string `json:"digest"`
			CodeReviewIdentity string `json:"code_review_identity"`
		} `json:"evaluation"`
	}
	if json.Unmarshal(target.ValidationContract, &c) != nil {
		return status.Error(codes.FailedPrecondition, "execution contract unavailable")
	}
	if len(c.Scripts) == 0 {
		return nil
	}
	identity := skillvalidation.Identity{Scripts: c.Scripts, ImageDigest: c.ImageDigest, EntryPoint: c.EntryPoint}
	raw, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if c.Digest != target.Digest || c.CodeReviewDigest != contentDigest(string(raw)) || c.CodeReviewIdentity == "" || !c.Evaluation.Passed || c.Evaluation.Status != "passed" || c.Evaluation.Digest != target.Digest || c.Evaluation.CodeReviewIdentity != c.CodeReviewIdentity {
		return status.Error(codes.FailedPrecondition, "exact script review and passed validation required")
	}
	admission, err := skillvalidation.Review(ctx, identity)
	if err != nil || admission != c.CodeReviewIdentity {
		return status.Error(codes.FailedPrecondition, "trusted execution admission changed")
	}
	var count int64
	if err := tx.Table("bot.skill_candidate_validations").Where("candidate_id = ? AND digest = ? AND status = 'completed' AND result->>'status' = 'passed' AND result->>'digest' = ? AND result->>'code_review_identity' = ?", c.ID, target.Digest, target.Digest, c.CodeReviewIdentity).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return status.Error(codes.FailedPrecondition, "exact script verification unavailable")
	}
	return nil
}

func lockBinding(tx *gorm.DB, botID, skillID, scope string) (*binding, error) {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-publish:"+skillID).Error; err != nil {
		return nil, err
	}
	var row binding
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("bot_id = ? AND skill_id = ? AND scope_key = ?", botID, skillID, scope).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

func auditVersion(tx *gorm.DB, target *version, actor, action, reason string, revision int64) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	return tx.Table("bot.skill_version_activities").Create(map[string]any{"id": id, "bot_id": target.BotID, "skill_id": target.SkillID, "scope_key": target.ScopeKey, "actor_id": actor, "action": action, "version_id": target.ID, "version": target.Version, "digest": target.Digest, "reason": reason, "binding_revision": revision, "created_at": time.Now()}).Error
}

// AdoptPublished is shared by both publication paths. A disabled current
// binding never silently falls back or re-enables itself on a later publish.
func AdoptPublished(ctx context.Context, tx *gorm.DB, botID, skillID, versionID, scope, actor string) (bool, error) {
	current, err := lockBinding(tx, botID, skillID, scope)
	if err != nil {
		return false, err
	}
	var target version
	if err := tx.Where("id = ? AND bot_id = ? AND skill_id = ? AND scope_key = ? AND status = 'published'", versionID, botID, skillID, scope).Take(&target).Error; err != nil {
		return false, err
	}
	if err := executionAdmission(ctx, tx, &target); err != nil {
		return false, err
	}
	if current == nil {
		id, err := identity.New()
		if err != nil {
			return false, err
		}
		current = &binding{ID: id, BotID: botID, SkillID: skillID, VersionID: target.ID, ScopeKey: scope, Enabled: true, Revision: 1, BlockedReasons: json.RawMessage("[]"), UpdatedAt: time.Now()}
		if err := tx.Create(current).Error; err != nil {
			return false, err
		}
		return true, auditVersion(tx, &target, actor, "publish", "initial approved publication", 1)
	}
	if current.VersionID == target.ID {
		return current.Enabled && target.Status == "published", nil
	}
	var previous version
	if err := tx.Where("id = ?", current.VersionID).Take(&previous).Error; err != nil {
		return false, err
	}
	if target.Version <= previous.Version {
		return false, nil
	}
	reasons := contractChanges(&previous, &target)
	if !current.Enabled || previous.Status != "published" {
		reasons = append(reasons, "disabled_binding")
	}
	if target.Version <= current.RollbackHoldVersion {
		reasons = append(reasons, "rollback_hold")
	}
	current.Revision++
	current.UpdatedAt = time.Now()
	action, reason := "auto_upgrade", "trusted contracts unchanged"
	if len(reasons) > 0 {
		current.BlockedVersionID = &target.ID
		current.BlockedReasons, err = json.Marshal(reasons)
		action, reason = "upgrade_blocked", strings.Join(reasons, ", ")
	} else {
		current.VersionID, current.Enabled, current.RollbackHoldVersion = target.ID, true, 0
		current.BlockedVersionID, current.BlockedReasons = nil, json.RawMessage("[]")
	}
	if err != nil {
		return false, err
	}
	if err := tx.Save(current).Error; err != nil {
		return false, err
	}
	return len(reasons) == 0, auditVersion(tx, &target, actor, action, reason, current.Revision)
}

func VersionEnabled(tx *gorm.DB, botID, versionID string) (bool, error) {
	var count int64
	err := tx.Table("bot.skill_bindings b").Joins("JOIN bot.skill_versions v ON v.id = b.version_id AND v.skill_id = b.skill_id AND v.scope_key = b.scope_key").Where("b.bot_id = ? AND b.version_id = ? AND b.enabled = true AND v.status = 'published'", botID, versionID).Count(&count).Error
	return count == 1, err
}

func PackageState(tx *gorm.DB, botID, skillID, scope string, canManage bool) (*PackageView, error) {
	var pkg packageRow
	if err := tx.Where("id = ? AND bot_id = ?", skillID, botID).Take(&pkg).Error; err != nil {
		return nil, err
	}
	view := &PackageView{SkillID: pkg.ID, Name: pkg.Name, License: pkg.License, PublisherID: pkg.PublisherID, ScopeKey: scope, CanManage: canManage, Versions: []VersionView{}, Candidates: []CandidateView{}, Activities: []VersionActivity{}}
	var current binding
	err := tx.Where("bot_id = ? AND skill_id = ? AND scope_key = ?", botID, skillID, scope).Take(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	view.BindingRevision, view.RollbackHold = current.Revision, current.RollbackHoldVersion > 0
	var rows []version
	if err := tx.Where("bot_id = ? AND skill_id = ? AND scope_key = ?", botID, skillID, scope).Order("version ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		enabled := current.Enabled && current.VersionID == row.ID && row.Status == "published"
		v := versionView(&row, enabled)
		view.Versions = append(view.Versions, *v)
		if enabled {
			view.EnabledVersion = v
		}
		if current.BlockedVersionID != nil && *current.BlockedVersionID == row.ID && row.Status == "published" {
			reasons := []string{}
			if err := json.Unmarshal(current.BlockedReasons, &reasons); err != nil {
				return nil, err
			}
			view.BlockedUpgrade = &BlockedUpgrade{Version: *v, Reasons: reasons}
		}
	}
	var activities []struct {
		Action, Reason, ActorID, VersionID, Digest string
		Version                                    int
		BindingRevision                            int64
		CreatedAt                                  time.Time
	}
	if err := tx.Table("bot.skill_version_activities").Where("bot_id = ? AND skill_id = ? AND scope_key = ?", botID, skillID, scope).Order("created_at DESC,id DESC").Limit(100).Find(&activities).Error; err != nil {
		return nil, err
	}
	for _, a := range activities {
		view.Activities = append(view.Activities, VersionActivity{Action: a.Action, Reason: a.Reason, ActorID: a.ActorID, VersionID: a.VersionID, Version: a.Version, Digest: a.Digest, BindingRevision: a.BindingRevision, CreatedAt: a.CreatedAt.Unix()})
	}
	return view, nil
}

// ChangeVersion is called only after runtime has checked live scope authority.
// Binding and publication use the same serialization point, including the
// watermark MAX query, so a rollback cannot race an already-published upgrade.
func ChangeVersion(ctx context.Context, tx *gorm.DB, botID, actor, action string, request VersionRequest) (*PackageView, error) {
	targetIdentity, err := FindVersion(tx, botID, request.VersionID)
	if err != nil {
		return nil, err
	}
	current, err := lockBinding(tx, botID, targetIdentity.SkillID, targetIdentity.ScopeKey)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, status.Error(codes.FailedPrecondition, "skill binding unavailable")
	}
	var target version
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", request.VersionID).Take(&target).Error; err != nil {
		return nil, err
	}
	if request.Digest != target.Digest || request.ExpectedRevision != current.Revision {
		return nil, status.Error(codes.Aborted, "skill binding revision or version digest changed; reload")
	}
	if target.Status != "published" {
		return nil, status.Error(codes.FailedPrecondition, "skill version is disabled")
	}
	switch action {
	case "confirm", "rollback":
		if err := executionAdmission(ctx, tx, &target); err != nil {
			return nil, err
		}
		var selected version
		if err := tx.Where("id = ?", current.VersionID).Take(&selected).Error; err != nil {
			return nil, err
		}
		if action == "rollback" && target.Version >= selected.Version {
			return nil, status.Error(codes.FailedPrecondition, "rollback requires an older approved version")
		}
		if target.Version < selected.Version {
			if err := tx.Model(&version{}).Where("bot_id = ? AND skill_id = ? AND scope_key = ?", botID, target.SkillID, target.ScopeKey).Select("COALESCE(MAX(version),0)").Scan(&current.RollbackHoldVersion).Error; err != nil {
				return nil, err
			}
		} else {
			current.RollbackHoldVersion = 0
		}
		current.VersionID, current.Enabled = target.ID, true
		current.BlockedVersionID, current.BlockedReasons = nil, json.RawMessage("[]")
	case "disable":
		if err := tx.Model(&version{}).Where("id = ?", target.ID).Update("status", "disabled").Error; err != nil {
			return nil, err
		}
		if current.VersionID == target.ID {
			current.Enabled = false
		}
		if current.BlockedVersionID != nil && *current.BlockedVersionID == target.ID {
			current.BlockedVersionID, current.BlockedReasons = nil, json.RawMessage("[]")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown version action")
	}
	current.Revision++
	current.UpdatedAt = time.Now()
	if err := tx.Save(current).Error; err != nil {
		return nil, err
	}
	if err := auditVersion(tx, &target, actor, action, request.Reason, current.Revision); err != nil {
		return nil, err
	}
	return PackageState(tx, botID, target.SkillID, target.ScopeKey, true)
}
