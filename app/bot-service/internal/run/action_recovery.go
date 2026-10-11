package run

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/component"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These guards run with the Run locked, so admission cannot race an action claim.
func unresolvedWriteAction(tx *gorm.DB, r *model.Run) error {
	return checkExternalWrite(tx, r, []string{"unknown"})
}

func pendingExternalWrite(tx *gorm.DB, r *model.Run) error {
	return checkExternalWrite(tx, r, []string{"executing", "unknown"})
}

func checkExternalWrite(tx *gorm.DB, r *model.Run, states []string) error {
	var count int64
	if err := tx.Model(&model.RunAction{}).Where("run_id = ? AND status IN ? AND risk NOT IN ('read', 'draft')", r.ID, states).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return status.Error(codes.FailedPrecondition, "external action requires human outcome reconciliation")
	}
	return nil
}

var errActionsUnchanged = errors.New("action recovery unchanged")

// recoverActions never infers an external outcome from a checkpoint or a lease.
// Only read/draft calls are safe to retry; an orphaned write is a durable unknown.
func (s *Service) recoverActions(ctx context.Context, r *model.Run) (*model.Run, error) {
	var count int64
	if err := s.svc.DB.WithContext(ctx).Model(&model.RunAction{}).Where("run_id = ? AND status IN ('executing', 'unknown')", r.ID).Count(&count).Error; err != nil {
		return r, err
	}
	if count == 0 {
		return r, nil
	}
	updated, err := s.repo.ChangeActionFacts(ctx, r.ID, func(tx *gorm.DB, current *model.Run) error {
		var actions []model.RunAction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ? AND status IN ('executing', 'unknown')", current.ID).Find(&actions).Error; err != nil {
			return err
		}
		changed, unknown := false, false
		for i := range actions {
			a := &actions[i]
			if a.Status == "executing" {
				// Concurrent accepted input belongs to the next complete boundary,
				// not to executor ownership of this already-dispatched action.
				orphan := a.Generation != current.ExecutionGeneration
				if !orphan {
					leaseErr := repo.CheckExecutionLease(tx, current, a.Generation)
					if leaseErr != nil && status.Code(leaseErr) != codes.Aborted {
						return leaseErr
					}
					orphan = leaseErr != nil
				}
				if !orphan {
					continue
				}
				if (a.Risk == "read" || a.Risk == "draft") && !repo.TerminalRun(current) {
					a.Status, a.ClaimedAt, a.CompletedAt, a.Result = "approved", nil, nil, ""
				} else {
					a.Status = "unknown"
					if !a.SourceInvalidated {
						a.Result = `{"status":"unknown","error":"executor lost before durable outcome receipt; human reconciliation required"}`
					}
				}
				if err := tx.Save(a).Error; err != nil {
					return err
				}
				changed = true
			}
			unknown = unknown || (a.Status == "unknown" && a.Risk != "read" && a.Risk != "draft")
		}
		if unknown && !repo.TerminalRun(current) && current.Status != "stopping" && (current.Status != "waiting_authorization" || current.Stage != "action_outcome_unknown" || current.WaitingReason != "action_outcome_unknown") {
			current.Status, current.Stage, current.WaitingReason = "waiting_authorization", "action_outcome_unknown", "action_outcome_unknown"
			current.StreamStatus = "closed"
			changed = true
		}
		if !changed {
			return errActionsUnchanged
		}
		return nil
	})
	if errors.Is(err, errActionsUnchanged) {
		return updated, nil
	}
	return updated, err
}

// Rebinding changes only the executor generation, never the action's accepted
// input, principal, provenance, deadline, denial or approval decision.
func (s *Service) rebindAction(tx *gorm.DB, r *model.Run, a *model.RunAction, frozen component.RunTool, server model.McpServer, digest string) error {
	priorStatus, priorGeneration := a.Status, a.Generation
	if err := s.actionSourcesCurrent(tx, r, a); err != nil {
		return err
	}
	if !actionPrincipalMatches(a, r) || a.RunID != r.ID || a.BotID != r.BotID || a.BindingID != r.BindingID || a.RequesterID != r.RequesterID || a.InputVersion != r.InputVersion {
		return status.Error(codes.PermissionDenied, "action input or principal changed")
	}
	var provenance, current []model.SourceVersion
	if json.Unmarshal(a.Provenance, &provenance) != nil || json.Unmarshal(r.Provenance, &current) != nil || !slices.Equal(provenance, current) {
		return status.Error(codes.PermissionDenied, "action provenance changed")
	}
	var definition component.RunTool
	if json.Unmarshal(a.ToolDefinition, &definition) != nil || a.ServerID != frozen.ServerID || a.ToolName != frozen.Name || a.Risk != frozen.Risk || a.DefinitionHash != frozen.DefinitionHash || a.ParametersHash != digest || definition.ServerID != frozen.ServerID || definition.Name != frozen.Name || definition.DefinitionHash != frozen.DefinitionHash || definition.Risk != frozen.Risk || definition.Transport != frozen.Transport || definition.URL != frozen.URL || definition.TargetParameter != frozen.TargetParameter || definition.CanShare != frozen.CanShare || !slices.Equal(definition.ConversationIDs, frozen.ConversationIDs) {
		return status.Error(codes.AlreadyExists, "action differs from its immutable definition or parameters")
	}
	if a.Status == "executing" || a.Status == "unknown" {
		return status.Error(codes.FailedPrecondition, "action outcome unresolved; action cannot be rebound")
	}
	if (a.Status == "pending" || a.Status == "approved") && !a.ExpiresAt.After(time.Now()) {
		a.Status = "expired"
	}
	if a.Status == "approved" && actionWrite(a.Risk) {
		if err := actionApprovalCurrent(tx, r, a, server); err != nil {
			if status.Code(err) != codes.PermissionDenied {
				return err
			}
			a.Status = "revoked"
		}
	}
	_, storedDigest, err := canonicalActionArguments(a.Arguments, server)
	if err != nil || storedDigest != digest {
		return status.Error(codes.PermissionDenied, "durable action parameters changed")
	}
	if a.Generation != r.ExecutionGeneration {
		a.Generation = r.ExecutionGeneration
		if a.Status == "pending" {
			r.Status, r.Stage, r.WaitingReason = "waiting_authorization", "pending_action", "action_approval_required"
		}
	}
	if a.Status == priorStatus && a.Generation == priorGeneration {
		return nil
	}
	return tx.Save(a).Error
}

func actionApprovalCurrent(tx *gorm.DB, r *model.Run, a *model.RunAction, server model.McpServer) error {
	if !a.ExpiresAt.After(time.Now()) || server.OwnerType != "user" || server.OwnerID == nil || *server.OwnerID != a.ApproverID {
		return status.Error(codes.PermissionDenied, "action approval expired or resource owner changed")
	}
	if a.GrantID == nil {
		return nil
	}
	var grant model.ActionGrant
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND revoked_at IS NULL AND expires_at > NOW()", *a.GrantID).First(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Error(codes.PermissionDenied, "automatic action permission expired or revoked")
		}
		return err
	}
	if !actionGrantPrincipal(tx, &grant, r) || grant.BotID != r.BotID || grant.ConvID != r.ConvID || grant.BindingID != r.BindingID || grant.RequesterID != r.RequesterID || grant.OwnerID != a.ApproverID || grant.ServerID != a.ServerID || grant.ToolName != a.ToolName || grant.ParametersHash != a.ParametersHash || grant.DefinitionHash != a.DefinitionHash || grant.BotAuthorizationVersion != r.BotAuthorizationVersion || grant.BindingAuthorizationVersion != r.BindingAuthorizationVersion {
		return status.Error(codes.PermissionDenied, "automatic action scope changed")
	}
	return nil
}
