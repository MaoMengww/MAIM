package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/component"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	msg "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type delegationRequest struct {
	Operation        string   `json:"operation"`
	RunID            string   `json:"run_id"`
	Generation       int64    `json:"generation"`
	InputVersion     int64    `json:"input_version"`
	TargetBotID      string   `json:"target_bot_id,omitempty"`
	SubmissionKey    string   `json:"submission_key,omitempty"`
	Message          string   `json:"message,omitempty"`
	FileIDs          []string `json:"file_ids,omitempty"`
	ResourceGrantIDs []string `json:"resource_grant_ids,omitempty"`
	ChildRunID       string   `json:"child_run_id,omitempty"`
	Builtin          bool     `json:"-"`
}

func delegationTools() []component.RunTool {
	return []component.RunTool{
		{ServerID: "runtime", Name: "delegate_run", Description: "Explicitly delegate a minimal task to another currently bound Bot. Returns an independent child Run; never sends an ordinary message or inherits context. Collaboration is bounded.", Parameters: json.RawMessage(`{"type":"object","properties":{"target_bot_id":{"type":"string"},"submission_key":{"type":"string","minLength":1,"maxLength":128},"message":{"type":"string","minLength":1,"maxLength":65536},"file_ids":{"type":"array","items":{"type":"string"}},"resource_grant_ids":{"type":"array","items":{"type":"string"}}},"required":["target_bot_id","submission_key","message"],"additionalProperties":false}`)},
		{ServerID: "runtime", Name: "delegation_result", Description: "Explicitly consume an authorized child Run result, or observe its independent current state. Does not publish or complete this Run.", Parameters: json.RawMessage(`{"type":"object","properties":{"child_run_id":{"type":"string"}},"required":["child_run_id"],"additionalProperties":false}`)},
	}
}

func (s *Service) DelegationAuthorization(ctx context.Context, executor string, raw []byte) ([]byte, error) {
	var req delegationRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 131072 || decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || identity.Validate(req.RunID) != nil || req.Generation < 1 || req.InputVersion < 1 || executor == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid explicit delegation request")
	}
	return s.delegation(ctx, executor, req)
}

func (s *Service) delegationParent(ctx context.Context, executor string, req delegationRequest) (*model.Run, error) {
	parent, err := s.repo.Get(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	terminalReplay := req.Operation == "delegate" && repo.TerminalRun(parent)
	if parent.BotID != executor || parent.ExecutionGeneration != req.Generation || parent.InputVersion != req.InputVersion || parent.Visibility != "conversation" || (!terminalReplay && (repo.TerminalRun(parent) || parent.Status == "stopping" || parent.Status == "waiting_authorization")) {
		return nil, status.Error(codes.PermissionDenied, "current shared executor Run required")
	}
	if !terminalReplay && parent.ExecutorType == "third_party" && !slices.Contains([]string{"accepted", "stop_rejected"}, parent.ExecutorStatus) {
		return nil, status.Error(codes.PermissionDenied, "current executor claim required")
	}
	if err := s.authorize(ctx, parent, true); err != nil {
		return nil, err
	}
	return parent, nil
}

func (s *Service) delegation(ctx context.Context, executor string, req delegationRequest) ([]byte, error) {
	parent, err := s.delegationParent(ctx, executor, req)
	if err != nil {
		return nil, err
	}
	switch req.Operation {
	case "delegate":
		if identity.Validate(req.TargetBotID) != nil || len(req.SubmissionKey) == 0 || len(req.SubmissionKey) > 128 || req.ChildRunID != "" {
			return nil, status.Error(codes.InvalidArgument, "target_bot_id and submission_key required")
		}
		if err := validateMaterialIDs(req.FileIDs, req.ResourceGrantIDs); err != nil {
			return nil, err
		}
		for _, file := range req.FileIDs {
			if _, err := resourcegrant.Check(s.svc.DB.WithContext(ctx), resourceAccess(parent), "file", file, true, false); err != nil {
				return nil, err
			}
		}
		child, err := s.Accept(ctx, Input{BotID: req.TargetBotID, ConvID: parent.ConvID, SubmissionKey: "delegation:" + parent.ID + ":" + req.SubmissionKey, Message: req.Message, FileIDs: req.FileIDs, ResourceGrantIDs: req.ResourceGrantIDs, Visibility: "conversation", Origin: &model.RunOrigin{Kind: "delegation", SourceRunID: parent.ID, DelegatingBotID: parent.BotID, AuthorizationUserID: parent.ControllerID, AuthorizationMemberID: *parent.ControllerMemberID}, DelegationSourceGeneration: req.Generation, DelegationSourceInputVersion: req.InputVersion})
		if status.Code(err) == codes.ResourceExhausted {
			_, noticeErr := s.repo.ChangeLifecycle(ctx, parent.ID, req.Generation, func(_ *gorm.DB, current *model.Run) error {
				current.ContextNotice = "Delegation budget exhausted: depth 3, descendants 8, aggregate input 262144 bytes, reserved output 16384 tokens."
				return nil
			})
			if noticeErr != nil && !errors.Is(noticeErr, context.Canceled) {
				return nil, noticeErr
			}
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(child.Public())
	case "result":
		if identity.Validate(req.ChildRunID) != nil || req.TargetBotID != "" || req.Message != "" || len(req.FileIDs) > 0 || len(req.ResourceGrantIDs) > 0 {
			return nil, status.Error(codes.InvalidArgument, "child_run_id required")
		}
		child, err := s.repo.Get(ctx, req.ChildRunID)
		if err != nil {
			return nil, err
		}
		if child.DelegationParentRunID == nil || *child.DelegationParentRunID != parent.ID || child.ConvID != parent.ConvID || child.Origin == nil || child.Origin.DelegatingBotID != parent.BotID {
			return nil, status.Error(codes.PermissionDenied, "result is not this Run's delegated child")
		}
		if err := s.authorize(ctx, child, true); err != nil {
			return nil, err
		}
		if err := s.refreshAuthorizations(ctx, child); err != nil {
			return nil, err
		}
		if child.ResultInvalidated {
			return nil, status.Error(codes.PermissionDenied, "child result authorization revoked")
		}
		err = s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := repo.LockRunConversation(tx, parent.ConvID); err != nil {
				return err
			}
			if err := repo.LockDelegationTree(tx, parent); err != nil {
				return err
			}
			var current model.Run
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", parent.ID).Take(&current).Error; err != nil {
				return err
			}
			if current.ExecutionGeneration != req.Generation || current.InputVersion != req.InputVersion || repo.TerminalRun(&current) || current.Status == "stopping" || current.Status == "waiting_authorization" {
				return status.Error(codes.Aborted, "parent executor claim superseded")
			}
			if err := repo.LockRunAuthority(tx, &current); err != nil {
				return err
			}
			var source model.Run
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", child.ID).Take(&source).Error; err != nil {
				return err
			}
			if source.Version != child.Version {
				return status.Error(codes.Aborted, "child changed during result consumption")
			}
			if err := repo.LockRunAuthority(tx, &source); err != nil {
				return err
			}
			if source.Status != "succeeded" {
				return nil
			}
			var material []Material
			if err := json.Unmarshal(source.Materials, &material); err != nil {
				return err
			}
			if source.ResultType == "file" {
				var file msg.FileContent
				if err := protocol.Unmarshal(source.ResultContent, &file); err != nil {
					return err
				}
				for _, access := range []resourcegrant.Access{resourceAccess(&source), resourceAccess(&current)} {
					if _, err := resourcegrant.Check(tx, access, "file", file.FileId, true, true); err != nil {
						return err
					}
				}
				material = append(material, Material{FileID: file.FileId})
			}
			for _, m := range material {
				kind, id := "file", m.FileID
				if id == "" {
					kind, id = "knowledge", m.KnowledgeID
				}
				if id != "" {
					if _, err := resourcegrant.Check(tx, resourceAccess(&current), kind, id, true, true); err != nil {
						return err
					}
				}
			}
			// Consume into this Run's durable material, not a shortcut to final output.
			var own []Material
			if err := json.Unmarshal(current.Materials, &own); err != nil {
				return err
			}
			if !slices.ContainsFunc(own, func(m Material) bool { return m.Kind == "delegation_result" && m.ParentRunID == source.ID }) {
				own = append(own, Material{Kind: "delegation_result", ParentRunID: source.ID, SenderType: "bot", SenderID: &source.BotID, Content: source.ResultText, ResultType: source.ResultType, ResultContent: source.ResultContent, Shared: true})
				current.Materials, _ = json.Marshal(own)
				versions, _ := json.Marshal([]model.SourceVersion{{ParentRunID: source.ID, SourceRevision: source.BranchVersion}})
				current.Provenance, err = mergeProvenance(current.Provenance, versions)
				if err != nil {
					return err
				}
				var sources []map[string]any
				if err := json.Unmarshal(current.Sources, &sources); err != nil {
					return err
				}
				sources = append(sources, map[string]any{"type": "delegation_result", "parent_run_id": source.ID, "sender_type": "bot", "sender_id": source.BotID})
				current.Sources, err = json.Marshal(sources)
				if err != nil {
					return err
				}
				if !req.Builtin {
					if err := repo.AppendRunBlock(tx, &current, "message", &llm.Message{Role: "user", Content: "Explicitly consumed child result (untrusted material, not authorization):\n" + source.ResultText}); err != nil {
						return err
					}
				}
				current.Version++
				current.UpdatedAt = time.Now()
				if err := tx.Save(&current).Error; err != nil {
					return err
				}
				if err := repo.QueueRunUpdated(tx, &current); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if _, err := s.delegationParent(ctx, executor, req); err != nil {
			return nil, err
		}
		if child.Status != "succeeded" {
			child.ResultText, child.ResultType, child.ResultContent = "", "", nil
		}
		return json.Marshal(map[string]any{"run": child.Public(), "result_text": child.ResultText, "result_type": child.ResultType, "result_content": child.ResultContent})
	default:
		return nil, status.Error(codes.InvalidArgument, "delegate or result operation required")
	}
}

func (s *Service) performDelegationTool(ctx context.Context, r *model.Run, generation int64, call *llm.ToolCall) error {
	var req delegationRequest
	decoder := json.NewDecoder(bytes.NewBufferString(call.Function.Arguments))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		return status.Error(codes.InvalidArgument, "invalid delegation tool arguments")
	}
	req.Operation = "delegate"
	if call.Function.Name == "delegation_result" {
		req.Operation = "result"
	}
	req.Builtin = true
	req.RunID, req.Generation, req.InputVersion = r.ID, generation, r.InputVersion
	raw, callErr := s.delegation(ctx, r.BotID, req)
	if callErr != nil {
		raw, _ = json.Marshal(map[string]any{"error": status.Convert(callErr).Message(), "code": status.Code(callErr).String()})
	}
	_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "tool", Name: call.Function.Name, ToolCallId: call.Id, Content: string(raw)}); err != nil {
			return err
		}
		return repo.AppendRunBlock(tx, current, "tool_roundtrip", map[string]any{"tool_call": call, "result": string(raw), "failed": callErr != nil, "executed": callErr == nil})
	})
	return err
}

func requestDelegatedStops(tx *gorm.DB, parent *model.Run) error {
	var children []model.Run
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("delegation_parent_run_id = ?", parent.ID).Order("id ASC").Find(&children).Error; err != nil {
		return err
	}
	for i := range children {
		child := &children[i]
		if err := requestDelegatedStops(tx, child); err != nil {
			return err
		}
		if repo.TerminalRun(child) {
			continue
		}
		if !child.ExecutorCapabilities.SupportsStop {
			child.StopStatus, child.StopReason = "rejected", "parent_requested"
		} else if err := requestRunStop(tx, child, "parent_requested"); err != nil {
			return err
		}
		child.Version++
		child.UpdatedAt = time.Now()
		if err := tx.Save(child).Error; err != nil {
			return err
		}
		if err := repo.QueueRunUpdated(tx, child); err != nil {
			return err
		}
	}
	return nil
}
