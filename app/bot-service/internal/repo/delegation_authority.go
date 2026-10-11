package repo

import (
	"encoding/json"
	"errors"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A source's participation lifecycle remains authority, not its IM visibility
// or mutable execution result. No source message is consulted here.
func CheckDelegationSource(tx *gorm.DB, child *model.Run) error {
	seen := map[string]bool{child.ID: true}
	for child.DelegationParentRunID != nil {
		if seen[*child.DelegationParentRunID] {
			return status.Error(codes.PermissionDenied, "delegation source ancestry is cyclic")
		}
		var source model.Run
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", *child.DelegationParentRunID).Take(&source).Error; err != nil {
			return status.Error(codes.PermissionDenied, "delegation source unavailable")
		}
		seen[source.ID] = true
		if source.ConvID != child.ConvID || child.Origin == nil || child.Origin.Kind != "delegation" || child.Origin.SourceRunID != source.ID || source.BotID != child.Origin.DelegatingBotID || source.Visibility != "conversation" || source.AccessRevoked {
			return status.Error(codes.PermissionDenied, "delegation source authorization revoked")
		}
		if source.Origin != nil {
			member, role, err := RunMember(tx, source.ConvID, source.Origin.AuthorizationUserID)
			if err != nil || member != source.Origin.AuthorizationMemberID || (source.Origin.Kind == "rule" && role > 2) {
				return status.Error(codes.PermissionDenied, "delegation source origin authorization revoked")
			}
		}
		var binding model.ConvBot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", source.BindingID, source.BotID, source.ConvID).Take(&binding).Error; err != nil {
			return status.Error(codes.PermissionDenied, "delegation source binding revoked")
		}
		if binding.AuthorizationVersion != source.BindingAuthorizationVersion {
			return status.Error(codes.PermissionDenied, "delegation source binding authorization revoked")
		}
		if enabled, exists := binding.BotSettings["enabled"]; exists && enabled != true {
			return status.Error(codes.PermissionDenied, "delegation source participation revoked")
		}
		var bot model.Bot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", source.BotID).Take(&bot).Error; err != nil || bot.AuthorizationVersion != source.BotAuthorizationVersion {
			return status.Error(codes.PermissionDenied, "delegating Bot authorization revoked")
		}
		if err := CheckDelegationGrants(tx, &source, child); err != nil {
			return err
		}
		child = &source
	}
	return nil
}

// CheckDelegationResult permits a consumed child result only in its delegating
// Run or explicit continuations of that Run within the same participation epoch.
func CheckDelegationResult(tx *gorm.DB, consumer, child *model.Run) error {
	if consumer.Visibility != "conversation" || child.Visibility != "conversation" || child.ConvID != consumer.ConvID || child.Status != "succeeded" || child.AccessRevoked || child.ResultInvalidated || child.DelegationParentRunID == nil || child.Origin == nil || child.Origin.Kind != "delegation" || child.Origin.SourceRunID != *child.DelegationParentRunID {
		return status.Error(codes.PermissionDenied, "delegated result is unavailable")
	}
	if err := lockRunPermissions(tx, child); err != nil {
		return err
	}
	access := resourcegrant.Access{BotID: child.BotID, ConvID: child.ConvID, BindingID: child.BindingID, RequesterID: child.RequesterID, Visibility: child.Visibility, GrantIDs: child.ResourceGrantIDs}
	if err := resourcegrant.CheckRunToolPermissions(tx, access, child.ID, true); err != nil {
		return err
	}
	var binding model.ConvBot
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", consumer.BindingID, consumer.BotID, consumer.ConvID).Take(&binding).Error; err != nil {
		return status.Error(codes.PermissionDenied, "delegation consumer binding revoked")
	}
	if binding.AuthorizationVersion != consumer.BindingAuthorizationVersion {
		return status.Error(codes.PermissionDenied, "delegation consumer authorization revoked")
	}
	if enabled, exists := binding.BotSettings["enabled"]; exists && enabled != true {
		return status.Error(codes.PermissionDenied, "delegation consumer participation revoked")
	}
	var interaction model.Interaction
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ?", consumer.InteractionID, consumer.BotID, consumer.ConvID, consumer.BindingID).Take(&interaction).Error; err != nil {
		return status.Error(codes.PermissionDenied, "delegation consumer interaction unavailable")
	}
	ancestor := consumer
	seen := make(map[string]bool)
	for {
		if seen[ancestor.ID] {
			return status.Error(codes.PermissionDenied, "continuation ancestry is cyclic")
		}
		seen[ancestor.ID] = true
		if ancestor.AccessRevoked || ancestor.BotID != consumer.BotID || ancestor.ConvID != consumer.ConvID || ancestor.BindingID != consumer.BindingID || ancestor.InteractionID != consumer.InteractionID || ancestor.Visibility != consumer.Visibility || ancestor.BotAuthorizationVersion != consumer.BotAuthorizationVersion || ancestor.BindingAuthorizationVersion != consumer.BindingAuthorizationVersion {
			return status.Error(codes.PermissionDenied, "continuation ancestry authorization changed")
		}
		if err := lockRunPermissions(tx, ancestor); err != nil {
			return err
		}
		if ancestor.ID == *child.DelegationParentRunID {
			if child.Origin.DelegatingBotID != ancestor.BotID {
				return status.Error(codes.PermissionDenied, "delegated result Bot does not match its parent")
			}
			if ancestor.ID != consumer.ID {
				var materials []struct {
					Kind        string `json:"kind"`
					ParentRunID string `json:"parent_run_id"`
				}
				if err := json.Unmarshal(ancestor.Materials, &materials); err != nil {
					return err
				}
				consumed := false
				for _, material := range materials {
					if material.Kind == "delegation_result" && material.ParentRunID == child.ID {
						consumed = true
						break
					}
				}
				if !consumed {
					return status.Error(codes.PermissionDenied, "ancestor did not consume this delegated result")
				}
			}
			return nil
		}
		if ancestor.ParentRunID == nil {
			return status.Error(codes.PermissionDenied, "result is not delegated to this continuation")
		}
		var parent model.Run
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", *ancestor.ParentRunID).Take(&parent).Error; err != nil {
			return status.Error(codes.PermissionDenied, "continuation ancestor unavailable")
		}
		if !TerminalRun(&parent) {
			return status.Error(codes.PermissionDenied, "continuation ancestor is not terminal")
		}
		ancestor = &parent
	}
}

func CheckDelegationGrants(tx *gorm.DB, parent, child *model.Run) error {
	for _, id := range child.ResourceGrantIDs {
		var grant resourcegrant.Grant
		found := false
		for _, kind := range []string{"file", "knowledge"} {
			err := tx.Table(resourcegrant.Table(kind)).Where("id = ?", id).Take(&grant).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := resourcegrant.Check(tx, resourcegrant.Access{BotID: parent.BotID, ConvID: parent.ConvID, BindingID: parent.BindingID, RequesterID: parent.RequesterID, Visibility: parent.Visibility, GrantIDs: parent.ResourceGrantIDs}, kind, grant.ResourceID, true, true); err != nil {
				return err
			}
			found = true
			break
		}
		if !found {
			return status.Error(codes.PermissionDenied, "target resource grant unavailable")
		}
	}
	return nil
}

// Acceptance reserves the first child call. Every later provider call reserves
// independently under the same root barrier; restarts never reset consumption.
func ReserveDelegationOutput(tx *gorm.DB, r *model.Run, tokens int64, first bool) error {
	if r.DelegationRootRunID == nil {
		return nil
	}
	if first {
		var calls int64
		if err := tx.Model(&model.RunModelCall{}).Where("run_id = ?", r.ID).Count(&calls).Error; err != nil {
			return err
		}
		if calls == 0 {
			return nil
		}
	}
	var reserved int64
	if err := tx.Model(&model.Run{}).Select("COALESCE(SUM(delegation_output_tokens),0)").Where("delegation_root_run_id = ?", *r.DelegationRootRunID).Scan(&reserved).Error; err != nil {
		return err
	}
	if tokens <= 0 || reserved+tokens > 16384 {
		return status.Error(codes.ResourceExhausted, "collaboration output reservation exhausted")
	}
	r.DelegationOutputTokens += tokens
	return nil
}

func ReserveDelegationInput(tx *gorm.DB, r *model.Run, bytes int64) error {
	if r.DelegationRootRunID == nil {
		return nil
	}
	var used int64
	if err := tx.Model(&model.Run{}).Select("COALESCE(SUM(delegation_input_bytes),0)").Where("delegation_root_run_id = ?", *r.DelegationRootRunID).Scan(&used).Error; err != nil {
		return err
	}
	if bytes < 0 || used+bytes > 262144 {
		return status.Error(codes.ResourceExhausted, "collaboration input budget exhausted")
	}
	r.DelegationInputBytes += bytes
	return nil
}
