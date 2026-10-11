package repo

import (
	"encoding/json"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func txInput(tx *gorm.DB, bot, binding, requester, key string) *gorm.DB {
	tx = tx.Where("bot_id = ? AND binding_id = ? AND input_key = ?", bot, binding, key)
	if requester == "" {
		return tx.Where("requester_id IS NULL")
	}
	return tx.Where("requester_id = ?", requester)
}

func txDelegationInput(tx *gorm.DB, parent, bot, key string) *gorm.DB {
	return tx.Where("bot_id = ? AND input_key = ? AND origin->>'kind' = 'delegation' AND origin->>'source_run_id' = ?", bot, key, parent)
}

// Every mutation takes the conversation barrier, then this durable barrier,
// before authority, interaction or Run row locks. Continuations and delegated
// descendants converge on the original consuming ancestor without changing the
// independent delegation budget identity of a continuation.
func LockDelegationTree(tx *gorm.DB, r *model.Run) error {
	ancestor := r
	seen := make(map[string]bool)
	for {
		if seen[ancestor.ID] {
			return status.Error(codes.PermissionDenied, "Run ancestry contains a cycle")
		}
		seen[ancestor.ID] = true
		var parentID *string
		continuation := ancestor.ParentRunID != nil
		if continuation {
			parentID = ancestor.ParentRunID
		} else if ancestor.DelegationRootRunID != nil {
			parentID = ancestor.DelegationRootRunID
		} else {
			parentID = ancestor.DelegationParentRunID
		}
		if parentID == nil {
			return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "delegation:"+ancestor.ID).Error
		}
		var parent model.Run
		if err := tx.Where("id = ?", *parentID).Take(&parent).Error; err != nil {
			return status.Error(codes.PermissionDenied, "Run ancestry unavailable")
		}
		if parent.ConvID != r.ConvID {
			return status.Error(codes.PermissionDenied, "Run ancestry is outside this conversation")
		}
		if continuation && (parent.InteractionID != ancestor.InteractionID || parent.BotID != ancestor.BotID || parent.BindingID != ancestor.BindingID || parent.Visibility != ancestor.Visibility || (parent.Visibility == "personal" && (parent.PersonalUserID == nil || ancestor.PersonalUserID == nil || *parent.PersonalUserID != *ancestor.PersonalUserID))) {
			return status.Error(codes.PermissionDenied, "parent source is outside this interaction")
		}
		ancestor = &parent
	}
}

func acceptDelegation(tx *gorm.DB, child *model.Run) error {
	var parent model.Run
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", *child.DelegationParentRunID).Take(&parent).Error; err != nil {
		return status.Error(codes.PermissionDenied, "delegation source unavailable")
	}
	if parent.ConvID != child.ConvID || parent.Visibility != "conversation" || parent.BotID != child.Origin.DelegatingBotID || parent.ExecutionGeneration != child.DelegationSourceGeneration || parent.InputVersion != child.DelegationSourceInputVersion {
		return status.Error(codes.PermissionDenied, "delegation source identity changed")
	}
	if TerminalRun(&parent) || parent.Status == "stopping" || parent.Status == "waiting_authorization" || parent.StopStatus != "none" || parent.ControllerID != child.ControllerID || parent.ControllerMemberID == nil || *parent.ControllerMemberID != child.Origin.AuthorizationMemberID {
		return status.Error(codes.FailedPrecondition, "delegation source suspended")
	}
	if parent.ExecutorType == "third_party" && parent.ExecutorStatus != "accepted" && parent.ExecutorStatus != "stop_rejected" {
		return status.Error(codes.PermissionDenied, "current executor claim required")
	}
	if err := lockRunAuthority(tx, &parent, true); err != nil {
		return err
	}
	rootID := parent.ID
	if parent.DelegationRootRunID != nil {
		rootID = *parent.DelegationRootRunID
	}
	var root model.Run
	if err := tx.Where("id = ?", rootID).Take(&root).Error; err != nil {
		return err
	}
	if root.Status == "stopping" || root.StopStatus != "none" || root.StopReason == "user_requested" {
		return status.Error(codes.FailedPrecondition, "collaboration stopping")
	}
	child.DelegationRootRunID, child.DelegationDepth = &rootID, parent.DelegationDepth+1
	for ancestor := parent; ; {
		if ancestor.BotID == child.BotID {
			return status.Error(codes.ResourceExhausted, "delegation cycle would repeat an ancestor Bot")
		}
		if ancestor.DelegationParentRunID == nil {
			break
		}
		var next model.Run
		if err := tx.Where("id = ?", *ancestor.DelegationParentRunID).Take(&next).Error; err != nil {
			return err
		}
		ancestor = next
	}
	var usage struct {
		Count  int64
		Bytes  int64
		Tokens int64
	}
	if err := tx.Model(&model.Run{}).Select("COUNT(*) AS count, COALESCE(SUM(delegation_input_bytes),0) AS bytes, COALESCE(SUM(delegation_output_tokens),0) AS tokens").Where("delegation_root_run_id = ?", rootID).Scan(&usage).Error; err != nil {
		return err
	}
	if child.DelegationDepth > 3 || usage.Count >= 8 || usage.Bytes+child.DelegationInputBytes > 262144 || usage.Tokens+child.DelegationOutputTokens > 16384 {
		return status.Error(codes.ResourceExhausted, "delegation budget exhausted: depth 3, descendants 8, input bytes 262144, reserved output tokens 16384")
	}
	if err := CheckDelegationGrants(tx, &parent, child); err != nil {
		return err
	}
	// Both principals must independently possess current read AND share rights.
	var target []struct {
		FileID      string `json:"file_id"`
		KnowledgeID string `json:"knowledge_id"`
	}
	if err := json.Unmarshal(child.Materials, &target); err != nil {
		return err
	}
	for _, material := range target {
		kind, id := "file", material.FileID
		if id == "" {
			kind, id = "knowledge", material.KnowledgeID
		}
		if id != "" {
			if _, err := resourcegrant.Check(tx, resourcegrant.Access{BotID: parent.BotID, ConvID: parent.ConvID, BindingID: parent.BindingID, RequesterID: parent.RequesterID, Visibility: parent.Visibility, GrantIDs: parent.ResourceGrantIDs}, kind, id, true, true); err != nil {
				return err
			}
		}
	}
	parent.ChildRunIDs = append(parent.ChildRunIDs, child.ID)
	parent.Version++
	if err := tx.Save(&parent).Error; err != nil {
		return err
	}
	return queueRun(tx, &parent, "updated")
}

// LockRunAuthority exposes the existing locked authorization boundary to runtime
// collaboration operations, never a second executor permission system.
func LockRunAuthority(tx *gorm.DB, r *model.Run) error { return lockRunAuthority(tx, r, true) }
