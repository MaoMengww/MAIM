package resourcegrant

import (
	"encoding/json"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxSourceAncestry = 64

type outputSourceRun struct {
	ID, BotID, ConvID, BindingID, InteractionID                         string
	RequesterID, Visibility, Status, ResultType                         string
	ControllerID                                                        string
	ControllerMemberID                                                  *string
	ParentRunID, DelegationParentRunID                                  *string
	ResourceGrantIDs                                                    []string `gorm:"serializer:json"`
	BranchVersion, BotAuthorizationVersion, BindingAuthorizationVersion int64
	AccessRevoked, ResultInvalidated                                    bool
	Origin, Provenance, Materials, ResultContent                        json.RawMessage
}

func (r *outputSourceRun) access() Access {
	return Access{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: r.RequesterID, Visibility: r.Visibility, GrantIDs: r.ResourceGrantIDs}
}

type outputSourceMaterial struct {
	Kind           string `json:"kind"`
	ParentRunID    string `json:"parent_run_id"`
	FileID         string `json:"file_id"`
	KnowledgeID    string `json:"knowledge_id"`
	MessageID      string `json:"message_id"`
	EditCount      int32  `json:"edit_count"`
	Seq            int64  `json:"seq"`
	HistoryGrantID string `json:"history_grant_id"`
}

type outputOrigin struct {
	Kind                  string `json:"kind"`
	SourceRunID           string `json:"source_run_id"`
	DelegatingBotID       string `json:"delegating_bot_id"`
	AuthorizationUserID   string `json:"authorization_user_id"`
	AuthorizationMemberID string `json:"authorization_member_id"`
}

// One traversal is shared by history, continuation and delegation edges. Source
// Runs always retain their own identity and grant IDs; reading is not a transfer.
// Only provenance with history fields rechecks message visibility: explicit
// accepted input snapshots remain durable facts.
type sourceFence struct {
	tx       *gorm.DB
	lock     bool
	visiting map[string]bool
	checked  map[string]bool
	history  map[string]bool
}

func newSourceFence(tx *gorm.DB, lock bool) *sourceFence {
	return &sourceFence{tx: tx, lock: lock, visiting: make(map[string]bool), checked: make(map[string]bool), history: make(map[string]bool)}
}

func sourceDenied(reason string) error { return status.Error(codes.PermissionDenied, reason) }

func (f *sourceFence) query(table string) *gorm.DB {
	query := f.tx.Table(table)
	if f.lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	return query
}

func (f *sourceFence) loadRun(convID, runID string) (*outputSourceRun, error) {
	var run outputSourceRun
	if identity.Validate(runID) != nil || f.query("bot.runs").Where("id = ? AND conv_id = ?", runID, convID).Take(&run).Error != nil {
		return nil, sourceDenied("related Run source unavailable")
	}
	return &run, nil
}

// CheckOutputSources is the common current-source fence for publication and
// history-derived consumption/projection. Publishing callers hold the
// conversation barrier and request row locks as well.
func CheckOutputSources(tx *gorm.DB, convID, bindingID, runID string, lock bool) error {
	fence := newSourceFence(tx, lock)
	run, err := fence.loadRun(convID, runID)
	if err != nil {
		return err
	}
	if run.BindingID != bindingID {
		return sourceDenied("Run output binding changed")
	}
	return fence.checkRun(run)
}

func (f *sourceFence) checkParticipation(run *outputSourceRun) error {
	var binding struct{ ID string }
	if err := f.query("messaging.conv_bots c").Select("c.id").Joins("JOIN bot.bots b ON b.id = c.bot_id").Where("c.id = ? AND c.conv_id = ? AND c.bot_id = ? AND c.revoked_at IS NULL AND b.status = 'active' AND c.authorization_version = ? AND b.authorization_version = ?", run.BindingID, run.ConvID, run.BotID, run.BindingAuthorizationVersion, run.BotAuthorizationVersion).Where("COALESCE(c.bot_settings::jsonb->'enabled', 'true'::jsonb) = 'true'::jsonb").Take(&binding).Error; err != nil {
		return sourceDenied("related Run participation authorization revoked")
	}
	return nil
}

// Completed shared results are material, not live work owned by their former
// controller. Current publication checks its own controller; source resources
// and tools retain their original requester/grant identity and member fences.
func (f *sourceFence) checkAuthority(run *outputSourceRun) error {
	if run.AccessRevoked || run.ResultInvalidated || run.Visibility != "conversation" {
		return sourceDenied("related Run output authorization revoked")
	}
	if err := f.checkParticipation(run); err != nil {
		return err
	}
	if err := CheckIDs(f.tx, run.access(), true, f.lock); err != nil {
		return err
	}
	return CheckRunToolPermissions(f.tx, run.access(), run.ID, f.lock)
}

// Only explicit continuation ancestry can inherit a consumed child result.
// ParentRunID is not delegation ancestry.
func (f *sourceFence) checkDelegationResult(consumer, child *outputSourceRun) error {
	var origin outputOrigin
	if child.Status != "succeeded" || child.AccessRevoked || child.ResultInvalidated || child.ConvID != consumer.ConvID || child.Visibility != "conversation" || consumer.Visibility != "conversation" || child.DelegationParentRunID == nil || json.Unmarshal(child.Origin, &origin) != nil || origin.Kind != "delegation" || origin.SourceRunID != *child.DelegationParentRunID {
		return sourceDenied("delegated result authorization changed")
	}
	var interaction struct{ ID string }
	if err := f.query("bot.interactions").Where("id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ? AND status = 'active'", consumer.InteractionID, consumer.BotID, consumer.ConvID, consumer.BindingID).Take(&interaction).Error; err != nil {
		return sourceDenied("delegated result interaction lifecycle revoked")
	}
	current := consumer
	seen := make(map[string]bool)
	for {
		if seen[current.ID] || len(seen) >= maxSourceAncestry || current.AccessRevoked || current.BotID != consumer.BotID || current.ConvID != consumer.ConvID || current.BindingID != consumer.BindingID || current.InteractionID != consumer.InteractionID || current.Visibility != consumer.Visibility || current.BotAuthorizationVersion != consumer.BotAuthorizationVersion || current.BindingAuthorizationVersion != consumer.BindingAuthorizationVersion {
			return sourceDenied("delegated result continuation lifecycle revoked")
		}
		seen[current.ID] = true
		if current.ID != consumer.ID && current.Status != "succeeded" && current.Status != "failed" && current.Status != "stopped" {
			return sourceDenied("delegated result continuation is not terminal")
		}
		if err := f.checkParticipation(current); err != nil {
			return err
		}
		if current.ID == *child.DelegationParentRunID && current.BotID == origin.DelegatingBotID {
			if current.ID != consumer.ID {
				var inherited []outputSourceMaterial
				if err := json.Unmarshal(current.Materials, &inherited); err != nil {
					return err
				}
				consumed := false
				for _, material := range inherited {
					if material.Kind == "delegation_result" && material.ParentRunID == child.ID {
						consumed = true
						break
					}
				}
				if !consumed {
					return sourceDenied("continuation has no consumed delegated result")
				}
			}
			break
		}
		if current.ParentRunID == nil {
			return sourceDenied("delegated result is outside explicit continuation")
		}
		var err error
		current, err = f.loadRun(consumer.ConvID, *current.ParentRunID)
		if err != nil {
			return err
		}
	}
	return nil // The child's origin is fenced by checkRun, using this traversal.
}

// Consumption grants never become durable sharing permission. Delegation (not
// history) requires both independent identities to share its material resources.
func (f *sourceFence) checkDelegationResources(consumer, child *outputSourceRun) error {
	var materials []outputSourceMaterial
	if err := json.Unmarshal(child.Materials, &materials); err != nil {
		return err
	}
	if child.ResultType == "file" {
		var file message.FileContent
		if err := protocol.Unmarshal(child.ResultContent, &file); err != nil {
			return err
		}
		materials = append(materials, outputSourceMaterial{FileID: file.FileId})
	}
	for _, material := range materials {
		for _, resource := range []struct{ kind, id string }{{"file", material.FileID}, {"knowledge", material.KnowledgeID}} {
			if resource.id == "" {
				continue
			}
			for _, access := range []Access{child.access(), consumer.access()} {
				if _, err := Check(f.tx, access, resource.kind, resource.id, true, f.lock); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (f *sourceFence) checkRun(run *outputSourceRun) error {
	if f.checked[run.ID] {
		return nil
	}
	if f.visiting[run.ID] || len(f.visiting)+len(f.history) >= maxSourceAncestry {
		return sourceDenied("cyclic or excessive Run output provenance")
	}
	f.visiting[run.ID] = true
	defer delete(f.visiting, run.ID)
	if err := f.checkAuthority(run); err != nil {
		return err
	}
	var versions []struct {
		KnowledgeSource
		HistorySource
		ParentRunID    string `json:"parent_run_id"`
		SourceRevision int64  `json:"source_revision"`
		FileID         string `json:"file_id"`
	}
	if err := json.Unmarshal(run.Provenance, &versions); err != nil {
		return err
	}
	var materials []outputSourceMaterial
	if err := json.Unmarshal(run.Materials, &materials); err != nil {
		return err
	}
	delegated, referenced := make(map[string]bool), make(map[string]bool)
	for _, material := range materials {
		if material.Kind == "delegation_result" {
			if identity.Validate(material.ParentRunID) != nil {
				return sourceDenied("invalid delegated result source")
			}
			delegated[material.ParentRunID] = true
		}
	}
	for _, version := range versions {
		if err := f.checkHistory(run.access(), version.HistorySource); err != nil {
			return err
		}
		for _, resource := range []struct{ kind, id string }{{"file", version.FileID}, {"knowledge", version.KnowledgeID}} {
			if resource.id != "" {
				if _, err := Check(f.tx, run.access(), resource.kind, resource.id, true, f.lock); err != nil {
					return err
				}
			}
		}
		if err := CheckKnowledgeSource(f.tx, version.KnowledgeSource, f.lock); err != nil {
			return err
		}
		if version.ParentRunID == "" {
			continue
		}
		parent, err := f.loadRun(run.ConvID, version.ParentRunID)
		if err != nil {
			return err
		}
		if parent.AccessRevoked || parent.ResultInvalidated || parent.BranchVersion != version.SourceRevision {
			return sourceDenied("parent source authorization changed")
		}
		if delegated[parent.ID] {
			if err := f.checkDelegationResult(run, parent); err != nil {
				return err
			}
			if err := f.checkDelegationResources(run, parent); err != nil {
				return err
			}
		} else if parent.BotID != run.BotID || parent.BindingID != run.BindingID || parent.InteractionID != run.InteractionID {
			return sourceDenied("parent source is outside this interaction")
		}
		if err := f.checkRun(parent); err != nil {
			return err
		}
		referenced[parent.ID] = true
	}
	for id := range delegated {
		if !referenced[id] {
			return sourceDenied("delegated result provenance unavailable")
		}
	}
	for _, material := range materials {
		if material.Kind == "conversation_history" {
			if material.HistoryGrantID == "" || material.Seq <= 0 {
				return sourceDenied("history material has no authorization provenance")
			}
			if err := f.checkHistory(run.access(), HistorySource{MessageID: material.MessageID, AcceptedEditCount: material.EditCount, HistoryGrantID: material.HistoryGrantID, HistorySeq: material.Seq}); err != nil {
				return err
			}
			continue
		}
		if material.HistoryGrantID != "" {
			grant, err := CheckHistoryGrant(f.tx, run.access(), material.HistoryGrantID, f.lock)
			if err != nil {
				return err
			}
			if material.Seq < grant.StartSeq || material.Seq > grant.EndSeq {
				return sourceDenied("history source authorization revoked")
			}
		}
	}
	// Sources are complete before following the reverse delegation-origin edge.
	// A legitimate delegator may already have consumed this child's result; that
	// edge is not a provenance cycle. Failures still invalidate the entire walk.
	f.checked[run.ID] = true
	if err := f.checkOrigin(run, 0); err != nil {
		delete(f.checked, run.ID)
		return err
	}
	return nil
}

func (f *sourceFence) checkOrigin(run *outputSourceRun, depth int) error {
	if len(run.Origin) == 0 || string(run.Origin) == "null" {
		return nil
	}
	var origin outputOrigin
	if err := json.Unmarshal(run.Origin, &origin); err != nil {
		return err
	}
	var authorizer struct {
		ID   string
		Role int32
	}
	if identity.Validate(origin.AuthorizationUserID) != nil || identity.Validate(origin.AuthorizationMemberID) != nil || f.query("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", run.ConvID, origin.AuthorizationUserID).Take(&authorizer).Error != nil || authorizer.ID != origin.AuthorizationMemberID || (origin.Kind == "rule" && authorizer.Role > 2) {
		return sourceDenied("delegation source authorizer revoked")
	}
	if origin.Kind == "delegation" {
		return f.checkDelegationOrigin(run.ConvID, origin.SourceRunID, origin.DelegatingBotID, depth)
	}
	return nil
}

func (f *sourceFence) checkDelegationOrigin(convID, runID, botID string, depth int) error {
	if depth >= 3 || identity.Validate(runID) != nil || identity.Validate(botID) != nil {
		return sourceDenied("invalid delegation output origin")
	}
	run, err := f.loadRun(convID, runID)
	if err != nil {
		return err
	}
	if run.BotID != botID {
		return sourceDenied("delegation source identity changed")
	}
	// A delegation origin is live authority, unlike a shared parent result.
	// Preserve its original controller's exact membership incarnation.
	var member struct{ ID string }
	if err := f.query("messaging.conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", run.ConvID, run.ControllerID).Take(&member).Error; err != nil || run.ControllerMemberID == nil || *run.ControllerMemberID != member.ID {
		return sourceDenied("delegation source controller lifecycle revoked")
	}
	if err := f.checkAuthority(run); err != nil {
		return err
	}
	if err := f.checkOrigin(run, depth+1); err != nil {
		return err
	}
	// The source graph currently checking this delegator already fences its
	// sources; do not turn the child's reverse origin edge into a false cycle.
	if !f.visiting[run.ID] {
		return f.checkRun(run)
	}
	return nil
}
