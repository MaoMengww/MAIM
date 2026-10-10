package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/maomeng/aim/app/bot-service/internal/component"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type actionRequest struct {
	Op            string          `json:"op"`
	RunID         string          `json:"run_id"`
	ActionID      string          `json:"action_id"`
	GrantID       string          `json:"grant_id"`
	BotID         string          `json:"bot_id"`
	ConvID        string          `json:"conv_id"`
	ServerID      string          `json:"server_id"`
	ToolName      string          `json:"tool_name"`
	Arguments     json.RawMessage `json:"arguments"`
	Generation    int64           `json:"generation"`
	InputVersion  int64           `json:"input_version"`
	SubmissionKey string          `json:"submission_key"`
	ExpiresAt     int64           `json:"expires_at"`
}

func decodeActionRequest(body []byte) (actionRequest, error) {
	var req actionRequest
	if len(body) > 65536 {
		return req, status.Error(codes.InvalidArgument, "action request too large")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, status.Error(codes.InvalidArgument, "invalid action request")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, status.Error(codes.InvalidArgument, "trailing action content")
	}
	if len(req.SubmissionKey) > 256 || len(req.ToolName) > 256 {
		return req, status.Error(codes.InvalidArgument, "action identity too large")
	}
	return req, nil
}

func actionWrite(risk string) bool {
	return slices.Contains([]string{"send", "modify", "delete", "pay"}, risk)
}
func secretParameter(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "credential") || strings.Contains(key, "api_key") || key == "authorization" || key == "cookie" || key == "private_key"
}
func redactActionValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		for k, item := range value {
			if secretParameter(k) {
				value[k] = "[redacted]"
			} else {
				value[k] = redactActionValue(item)
			}
		}
	case []any:
		for i, item := range value {
			value[i] = redactActionValue(item)
		}
	case string:
		var nested any
		dec := json.NewDecoder(strings.NewReader(value))
		dec.UseNumber()
		if dec.Decode(&nested) == nil {
			raw, _ := json.Marshal(redactActionValue(nested))
			return string(raw)
		}
	}
	return v
}

func actionValueContainsSecret(value any, secret string) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if strings.Contains(key, secret) || actionValueContainsSecret(item, secret) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if actionValueContainsSecret(item, secret) {
				return true
			}
		}
	case string:
		if strings.Contains(value, secret) {
			return true
		}
		var nested any
		if json.Unmarshal([]byte(value), &nested) == nil {
			return actionValueContainsSecret(nested, secret)
		}
	}
	return false
}
func canonicalActionArguments(raw []byte, server model.McpServer) (json.RawMessage, string, error) {
	var args map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil || args == nil {
		return nil, "", status.Error(codes.InvalidArgument, "action parameters must be a JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, "", status.Error(codes.InvalidArgument, "trailing action parameters")
	}
	var containsSecret func(any) bool
	containsSecret = func(v any) bool {
		switch value := v.(type) {
		case map[string]any:
			for key, item := range value {
				if secretParameter(key) || containsSecret(item) {
					return true
				}
			}
		case []any:
			for _, item := range value {
				if containsSecret(item) {
					return true
				}
			}
		case string:
			var nested any
			if json.Unmarshal([]byte(value), &nested) == nil {
				return containsSecret(nested)
			}
		}
		return false
	}
	if containsSecret(args) {
		return nil, "", status.Error(codes.InvalidArgument, "credentials must use transient tool configuration")
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return nil, "", err
	}
	if server.AuthConfig != nil {
		for _, secret := range []string{server.AuthConfig.APIKey, server.AuthConfig.Token} {
			if secret != "" && actionValueContainsSecret(args, secret) {
				return nil, "", status.Error(codes.InvalidArgument, "credentials cannot enter action records")
			}
		}
	}
	digest, err := hash(args)
	return canonical, digest, err
}

func (s *Service) actionOwner(ctx context.Context, serverID, user string) bool {
	if identity.Validate(user) != nil {
		return false
	}
	var server model.McpServer
	if s.svc.DB.WithContext(ctx).Where("id = ? AND enabled = true", serverID).First(&server).Error != nil {
		return false
	}
	// Platform resources have no existing platform-administrator API. Fail closed.
	return server.OwnerType == "user" && server.OwnerID != nil && *server.OwnerID == user
}
func (s *Service) publicAction(ctx context.Context, a *model.RunAction, user string) model.PublicAction {
	state := a.Status
	if (state == "pending" || state == "approved") && !a.ExpiresAt.After(time.Now()) {
		state = "expired"
	}
	r, err := s.repo.Get(ctx, a.RunID)
	owner := s.actionOwner(ctx, a.ServerID, user)
	personalRequester := err == nil && r.Visibility == "personal" && r.PersonalUserID != nil && *r.PersonalUserID == user
	valid := err == nil && s.actionDisclosure(ctx, r, a, user) == nil
	can := owner && valid && state == "pending" && !repo.TerminalRun(r) && r.Status != "stopping"
	parameters := json.RawMessage(`{"redacted":true}`)
	target := "[redacted]"
	if personalRequester || owner && valid {
		var value any
		dec := json.NewDecoder(bytes.NewReader(a.Arguments))
		dec.UseNumber()
		if dec.Decode(&value) == nil {
			parameters, _ = json.Marshal(redactActionValue(value))
		}
		target = a.Target
	}
	result := ""
	if (personalRequester || owner && valid && r.Visibility != "personal") && slices.Contains([]string{"succeeded", "failed", "unknown", "denied", "revoked"}, state) {
		result = a.Result
		var value any
		var server model.McpServer
		_ = s.svc.DB.WithContext(ctx).Where("id = ?", a.ServerID).First(&server).Error
		dec := json.NewDecoder(strings.NewReader(result))
		dec.UseNumber()
		if dec.Decode(&value) == nil {
			value = redactActionValue(value)
			if server.AuthConfig != nil {
				for _, secret := range []string{server.AuthConfig.APIKey, server.AuthConfig.Token} {
					if secret != "" {
						value = redactActionSecret(value, secret)
					}
				}
			}
			redacted, marshalErr := json.Marshal(value)
			if marshalErr == nil {
				result = string(redacted)
			} else {
				result = ""
			}
		} else if server.AuthConfig != nil {
			for _, secret := range []string{server.AuthConfig.APIKey, server.AuthConfig.Token} {
				if secret != "" {
					result = strings.ReplaceAll(result, secret, "[redacted]")
				}
			}
		}
	}
	canShare := personalRequester && state == "pending" && !repo.TerminalRun(r) && r.Status != "stopping" && s.authorize(ctx, r, false) == nil && s.privateConfirmationSources(s.svc.DB.WithContext(ctx), r) == nil && s.currentActionScope(s.svc.DB.WithContext(ctx), r, a) == nil
	return model.PublicAction{ID: a.ID, RunID: a.RunID, ToolName: a.ToolName, ServerID: a.ServerID, Risk: a.Risk, Target: target, Parameters: parameters, ParametersJSON: string(parameters), Status: state, ExpiresAt: a.ExpiresAt.Unix(), Generation: a.Generation, InputVersion: a.InputVersion, CanApprove: can, Result: result, ConfirmationShared: a.ConfirmationSharedAt != nil, CanShareConfirmation: canShare}
}
func (s *Service) decorateActions(ctx context.Context, run *model.PublicRun, user string) {
	var actions []model.RunAction
	if s.svc.DB.WithContext(ctx).Where("run_id = ?", run.ID).Order("created_at,id").Find(&actions).Error != nil {
		return
	}
	run.Actions = make([]model.PublicAction, 0, len(actions))
	for i := range actions {
		run.Actions = append(run.Actions, s.publicAction(ctx, &actions[i], user))
	}
}

// A confirmation discloses only this action, never its Run or private results.
func (s *Service) actionDisclosure(ctx context.Context, r *model.Run, a *model.RunAction, user string) error {
	tx := s.svc.DB.WithContext(ctx)
	if _, _, err := repo.RunMember(tx, r.ConvID, user); err != nil {
		return err
	}
	if r.Visibility == "personal" && (r.PersonalUserID == nil || *r.PersonalUserID != user) {
		if a.ConfirmationSharedAt == nil || a.ConfirmationOwnerID == nil || *a.ConfirmationOwnerID != user || !s.actionOwner(ctx, a.ServerID, user) {
			return status.Error(codes.PermissionDenied, "private action confirmation is not shared")
		}
		if err := s.privateConfirmationSources(tx, r); err != nil {
			return err
		}
	} else if r.Visibility != "personal" {
		if err := s.visible(ctx, user, r); err != nil {
			return err
		}
	}
	if err := s.authorize(ctx, r, false); err != nil {
		return err
	}
	return s.currentActionScope(tx, r, a)
}

func (s *Service) currentActionScope(tx *gorm.DB, r *model.Run, a *model.RunAction) error {
	if r.AccessRevoked || r.Status == "stopping" || a.RunID != r.ID || a.BotID != r.BotID || a.BindingID != r.BindingID || a.RequesterID != r.RequesterID || a.Generation != r.ExecutionGeneration || a.InputVersion != r.InputVersion || !actionPrincipalMatches(a, r) || !a.ExpiresAt.After(time.Now()) {
		return status.Error(codes.PermissionDenied, "action scope is no longer current")
	}
	var frozen component.RunTool
	if json.Unmarshal(a.ToolDefinition, &frozen) != nil || frozen.ServerID != a.ServerID || frozen.Name != a.ToolName || frozen.DefinitionHash != a.DefinitionHash || frozen.Risk != a.Risk {
		return status.Error(codes.PermissionDenied, "action definition changed")
	}
	server, err := currentActionTool(tx, r, frozen)
	if err != nil {
		return err
	}
	_, digest, err := canonicalActionArguments(a.Arguments, server)
	if err != nil || digest != a.ParametersHash {
		return status.Error(codes.PermissionDenied, "action parameters changed")
	}
	return nil
}

// Requesters may disclose their own source-derived parameters, not re-share
// another resource owner's material through a private tool confirmation.
func (s *Service) privateConfirmationSources(tx *gorm.DB, r *model.Run) error {
	if r.Visibility != "personal" || r.PersonalUserID == nil || *r.PersonalUserID != r.RequesterID || r.ControllerID != r.RequesterID {
		return status.Error(codes.PermissionDenied, "private confirmation requester required")
	}
	member, _, err := repo.RunMember(tx, r.ConvID, r.RequesterID)
	if err != nil || r.ControllerMemberID == nil || *r.ControllerMemberID != member {
		return status.Error(codes.PermissionDenied, "private confirmation principal changed")
	}
	ids := slices.Clone(r.ResourceGrantIDs)
	slices.Sort(ids)
	for _, id := range ids {
		found := false
		for _, kind := range []string{"file", "knowledge"} {
			var grant resourcegrant.Grant
			err := tx.Table(resourcegrant.Table(kind)).Where("id = ?", id).Take(&grant).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			access := resourceAccess(r)
			access.GrantIDs = []string{id}
			current, err := resourcegrant.Check(tx, access, kind, grant.ResourceID, false, true)
			if err != nil {
				return err
			}
			if current.OwnerID != r.RequesterID {
				return status.Error(codes.PermissionDenied, "private action cannot re-share another owner's resource")
			}
			found = true
			break
		}
		if !found {
			return status.Error(codes.PermissionDenied, "private confirmation source unavailable")
		}
	}
	return nil
}

func redactActionSecret(value any, secret string) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if strings.Contains(key, secret) {
				delete(value, key)
				continue
			}
			value[key] = redactActionSecret(item, secret)
		}
	case []any:
		for i, item := range value {
			value[i] = redactActionSecret(item, secret)
		}
	case string:
		var nested any
		dec := json.NewDecoder(strings.NewReader(value))
		dec.UseNumber()
		if dec.Decode(&nested) == nil {
			encoded, err := json.Marshal(redactActionSecret(nested, secret))
			if err == nil {
				return string(encoded)
			}
		}
		return strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}

// actionFence is rechecked under the Run lock, taking the same participation rows
// as normal execution. A checkpoint, Bot ownership or conversation role is not approval.
func (s *Service) actionFence(tx *gorm.DB, r *model.Run, generation, input int64) error {
	if repo.TerminalRun(r) || r.Status == "stopping" || r.AccessRevoked || r.ExecutionGeneration != generation || r.InputVersion != input {
		return status.Error(codes.Aborted, "action execution authority superseded")
	}
	if r.Status == "waiting_authorization" && r.Stage != "pending_action" {
		return status.Error(codes.FailedPrecondition, "Run requires explicit takeover")
	}
	var bot model.Bot
	var binding model.ConvBot
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", r.BotID).First(&bot).Error; err != nil {
		return status.Error(codes.PermissionDenied, "Bot authority revoked")
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND revoked_at IS NULL", r.BindingID, r.BotID, r.ConvID).First(&binding).Error; err != nil {
		return status.Error(codes.PermissionDenied, "binding revoked")
	}
	if bot.AuthorizationVersion != r.BotAuthorizationVersion || binding.AuthorizationVersion != r.BindingAuthorizationVersion {
		return status.Error(codes.PermissionDenied, "participation authority changed")
	}
	if enabled, ok := binding.BotSettings["enabled"]; ok && enabled != true {
		return status.Error(codes.PermissionDenied, "participation disabled")
	}
	member, _, err := repo.RunMember(tx, r.ConvID, r.ControllerID)
	if err != nil || r.ControllerMemberID == nil || *r.ControllerMemberID != member {
		return status.Error(codes.PermissionDenied, "controller membership lost")
	}
	if err := resourcegrant.CheckIDs(tx, resourceAccess(r), r.Visibility != "personal", true); err != nil {
		return err
	}
	return nil
}

func actionPrincipalMatches(a *model.RunAction, r *model.Run) bool {
	return r.ControllerMemberID != nil && a.ControllerID == r.ControllerID && a.ControllerMemberID == *r.ControllerMemberID
}

func actionGrantPrincipal(tx *gorm.DB, grant *model.ActionGrant, r *model.Run) bool {
	if grant.RequesterID != r.RequesterID || r.ControllerID != grant.RequesterID {
		return false
	}
	member, _, err := repo.RunMember(tx, r.ConvID, grant.RequesterID)
	return err == nil && member == grant.SubjectMemberID && r.ControllerMemberID != nil && *r.ControllerMemberID == member
}
func currentActionTool(tx *gorm.DB, r *model.Run, frozen component.RunTool) (model.McpServer, error) {
	var server model.McpServer
	if frozen.ServerID == "builtin" {
		if frozen.Name != "web_search" || frozen.Risk != "read" {
			return server, status.Error(codes.PermissionDenied, "unknown builtin risk")
		}
		return server, nil
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND enabled = true", frozen.ServerID).First(&server).Error; err != nil {
		return server, status.Error(codes.PermissionDenied, "tool resource revoked")
	}
	var association model.BotMcpServer
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("bot_id = ? AND mcp_server_id = ? AND enabled = true", r.BotID, server.ID).First(&association).Error; err != nil {
		return server, status.Error(codes.PermissionDenied, "tool assignment revoked")
	}
	ref := resourcegrant.ToolRef{ServerID: frozen.ServerID, Transport: frozen.Transport, URL: frozen.URL, Name: frozen.Name, DefinitionHash: frozen.DefinitionHash, Risk: frozen.Risk, TargetParameter: frozen.TargetParameter, CanShare: frozen.CanShare, ConversationIDs: frozen.ConversationIDs}
	if err := resourcegrant.CheckToolPermissions(tx, resourceAccess(r), []resourcegrant.ToolRef{ref}, true); err != nil {
		return server, err
	}
	return server, nil
}
func (s *Service) actionTool(ctx context.Context, r *model.Run, serverID, name string) (component.RunTool, model.McpServer, error) {
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return component.RunTool{}, model.McpServer{}, err
	}
	// Third-party executors receive no external credentials; AIM resolves its tools.
	tools := config.Tools
	if r.ExecutorType == "third_party" {
		servers, err := s.svc.BotRepo.ListEnabledMcpServers(ctx, r.BotID)
		if err != nil {
			return component.RunTool{}, model.McpServer{}, err
		}
		tools, err = component.DiscoverRunTools(ctx, servers)
		if err != nil {
			return component.RunTool{}, model.McpServer{}, err
		}
	}
	index := slices.IndexFunc(tools, func(t component.RunTool) bool { return t.ServerID == serverID && t.Name == name })
	if index < 0 {
		return component.RunTool{}, model.McpServer{}, status.Error(codes.PermissionDenied, "unknown or unassigned tool")
	}
	frozen := tools[index]
	server, err := currentActionTool(s.svc.DB.WithContext(ctx), r, frozen)
	return frozen, server, err
}

func (s *Service) prepareAction(ctx context.Context, r *model.Run, frozen component.RunTool, arguments, submission string, expires int64) (*model.RunAction, error) {
	if submission == "" {
		return nil, status.Error(codes.InvalidArgument, "action submission key required")
	}
	var result model.RunAction
	_, err := s.repo.ChangeLifecycle(ctx, r.ID, r.ExecutionGeneration, func(tx *gorm.DB, current *model.Run) error {
		if err := s.actionFence(tx, current, r.ExecutionGeneration, r.InputVersion); err != nil {
			return err
		}
		server, err := currentActionTool(tx, current, frozen)
		if err != nil {
			return err
		}
		canonical, digest, err := canonicalActionArguments([]byte(arguments), server)
		if err != nil {
			return err
		}
		priorErr := tx.Where("run_id = ? AND submission_key = ?", r.ID, submission).First(&result).Error
		if priorErr == nil {
			if !actionPrincipalMatches(&result, current) {
				return status.Error(codes.PermissionDenied, "action principal changed")
			}
			if result.ParametersHash != digest || result.DefinitionHash != frozen.DefinitionHash || result.Generation != r.ExecutionGeneration || result.InputVersion != r.InputVersion {
				return status.Error(codes.AlreadyExists, "action submission differs from its exact authorization")
			}
			return nil
		}
		if !errors.Is(priorErr, gorm.ErrRecordNotFound) {
			return priorErr
		}
		if current.Status == "waiting_authorization" {
			return status.Error(codes.FailedPrecondition, "pending action must be decided before another request")
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		var args map[string]json.RawMessage
		_ = json.Unmarshal(canonical, &args)
		target := ""
		if frozen.TargetParameter != "" {
			if json.Unmarshal(args[frozen.TargetParameter], &target) != nil || strings.TrimSpace(target) == "" || len(target) > 2048 {
				return status.Error(codes.InvalidArgument, "action requires a bounded concrete string target")
			}
		}
		if actionWrite(frozen.Risk) && frozen.TargetParameter == "" {
			return status.Error(codes.InvalidArgument, "write action requires a configured concrete target")
		}
		result = model.RunAction{ID: id, RunID: r.ID, BotID: r.BotID, BindingID: r.BindingID, RequesterID: r.RequesterID, ServerID: frozen.ServerID, ToolName: frozen.Name, DefinitionHash: frozen.DefinitionHash, ParametersHash: digest, Arguments: canonical, Target: target, Risk: frozen.Risk, SubmissionKey: submission, Generation: r.ExecutionGeneration, InputVersion: r.InputVersion, Status: "approved", ExpiresAt: time.Now().Add(15 * time.Minute)}
		result.ControllerID, result.ControllerMemberID = current.ControllerID, *current.ControllerMemberID
		result.ToolDefinition, err = json.Marshal(frozen)
		if err != nil {
			return err
		}
		if expires != 0 {
			expiry := time.Unix(expires, 0)
			if !expiry.After(time.Now()) || expiry.After(result.ExpiresAt) {
				return status.Error(codes.InvalidArgument, "action request expiry must be within fifteen minutes")
			}
			result.ExpiresAt = expiry
		}
		if actionWrite(frozen.Risk) {
			result.Status = "pending"
			var grants []model.ActionGrant
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("bot_id = ? AND binding_id = ? AND requester_id = ? AND server_id = ? AND tool_name = ? AND definition_hash = ? AND parameters_hash = ? AND revoked_at IS NULL AND expires_at > NOW() AND bot_authorization_version = ? AND binding_authorization_version = ?", r.BotID, r.BindingID, r.RequesterID, frozen.ServerID, frozen.Name, frozen.DefinitionHash, digest, r.BotAuthorizationVersion, r.BindingAuthorizationVersion).Find(&grants).Error; err != nil {
				return err
			}
			for _, grant := range grants {
				if server.OwnerType == "user" && server.OwnerID != nil && *server.OwnerID == grant.OwnerID && actionGrantPrincipal(tx, &grant, current) {
					result.Status = "approved"
					result.ApproverID = grant.OwnerID
					result.GrantID = &grant.ID
					if grant.ExpiresAt.Before(result.ExpiresAt) {
						result.ExpiresAt = grant.ExpiresAt
					}
					break
				}
			}
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if result.Status == "pending" {
			current.Status = "waiting_authorization"
			current.Stage = "pending_action"
			current.WaitingReason = "action_approval_required"
		}
		return nil
	})
	return &result, err
}

// invokeAction claims once before crossing the external boundary. An ambiguous
// RPC or worker death can never cause an external write to be silently replayed.
func (s *Service) invokeAction(ctx context.Context, r *model.Run, a *model.RunAction, frozen component.RunTool) (string, error) {
	var server model.McpServer
	replay := false
	_, err := s.repo.Change(ctx, r.ID, r.ExecutionGeneration, func(tx *gorm.DB, current *model.Run) error {
		if err := s.actionFence(tx, current, a.Generation, a.InputVersion); err != nil {
			return err
		}
		var err error
		server, err = currentActionTool(tx, current, frozen)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", a.ID).First(a).Error; err != nil {
			return err
		}
		if !actionPrincipalMatches(a, current) || a.Generation != current.ExecutionGeneration || a.InputVersion != current.InputVersion || a.BindingID != current.BindingID || a.RequesterID != current.RequesterID || a.DefinitionHash != frozen.DefinitionHash {
			return status.Error(codes.Aborted, "stale action scope")
		}
		_, digest, err := canonicalActionArguments(a.Arguments, server)
		if err != nil || digest != a.ParametersHash {
			return status.Error(codes.PermissionDenied, "action parameters changed")
		}
		switch a.Status {
		case "succeeded", "failed":
			replay = true
			return nil
		case "executing", "unknown":
			return status.Error(codes.FailedPrecondition, "action outcome unknown; external write will not be replayed")
		case "denied", "revoked":
			replay = true
			a.Result = `{"status":"` + a.Status + `"}`
			return nil
		case "approved":
		default:
			return status.Error(codes.FailedPrecondition, "concrete action is not approved")
		}
		if !a.ExpiresAt.After(time.Now()) {
			return status.Error(codes.PermissionDenied, "action authorization expired")
		}
		if actionWrite(a.Risk) {
			if server.OwnerType != "user" || server.OwnerID == nil || *server.OwnerID != a.ApproverID {
				return status.Error(codes.PermissionDenied, "action approver no longer owns resource")
			}
			if a.GrantID != nil {
				var grant model.ActionGrant
				if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND revoked_at IS NULL AND expires_at > NOW()", *a.GrantID).First(&grant).Error; err != nil {
					return status.Error(codes.PermissionDenied, "automatic action permission revoked")
				}
				if !actionGrantPrincipal(tx, &grant, current) || grant.BindingID != current.BindingID || grant.RequesterID != current.RequesterID || grant.ParametersHash != a.ParametersHash || grant.DefinitionHash != a.DefinitionHash {
					return status.Error(codes.PermissionDenied, "automatic action scope changed")
				}
			}
		}
		now := time.Now()
		a.Status = "executing"
		a.ClaimedAt = &now
		current.Stage = "tool"
		return tx.Save(a).Error
	})
	if err != nil {
		return "", err
	}
	if replay {
		return a.Result, nil
	}
	toolCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var result string
	var callErr error
	beforeCall := func(callCtx context.Context) error {
		if _, err := s.executionBoundary(callCtx, r, a.Generation); err != nil {
			return err
		}
		_, err := s.repo.Change(callCtx, r.ID, a.Generation, func(tx *gorm.DB, current *model.Run) error {
			if err := s.actionFence(tx, current, a.Generation, a.InputVersion); err != nil {
				return err
			}
			liveServer, err := currentActionTool(tx, current, frozen)
			if err != nil {
				return err
			}
			var live model.RunAction
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'executing'", a.ID).First(&live).Error; err != nil {
				return status.Error(codes.PermissionDenied, "action execution claim unavailable")
			}
			if !actionPrincipalMatches(&live, current) || !live.ExpiresAt.After(time.Now()) || live.ParametersHash != a.ParametersHash || live.Generation != current.ExecutionGeneration || live.InputVersion != current.InputVersion {
				return status.Error(codes.PermissionDenied, "claimed action scope superseded")
			}
			if actionWrite(live.Risk) && (liveServer.OwnerType != "user" || liveServer.OwnerID == nil || *liveServer.OwnerID != live.ApproverID) {
				return status.Error(codes.PermissionDenied, "claimed action resource ownership changed")
			}
			if live.GrantID != nil {
				var grant model.ActionGrant
				if tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND revoked_at IS NULL AND expires_at > NOW()", *live.GrantID).First(&grant).Error != nil {
					return status.Error(codes.PermissionDenied, "action permission revoked before dispatch")
				}
				if !actionGrantPrincipal(tx, &grant, current) {
					return status.Error(codes.PermissionDenied, "automatic action principal changed before dispatch")
				}
			}
			return nil
		})
		return err
	}
	if frozen.ServerID == "builtin" {
		callErr = beforeCall(toolCtx)
		if callErr == nil {
			result, callErr = component.CallBuiltinRunTool(toolCtx, frozen, string(a.Arguments))
		}
	} else {
		result, callErr = component.CallRunTool(toolCtx, server, frozen, string(a.Arguments), beforeCall)
	}
	state := "succeeded"
	if callErr != nil {
		state = "unknown"
		if !actionWrite(a.Risk) {
			state = "failed"
		}
		result = `{"status":"` + state + `","error":"tool invocation did not produce a confirmed success"}`
	}
	if len(result) > 131072 {
		result = `{"status":"` + state + `","result":"omitted: tool result exceeded work budget"}`
	}
	var decoded any
	if json.Unmarshal([]byte(result), &decoded) == nil {
		raw, _ := json.Marshal(redactActionValue(decoded))
		result = string(raw)
	}
	// Record actual/unknown outcome even if stop/revocation superseded the Run.
	receiptCtx, receiptCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer receiptCancel()
	now := time.Now()
	updated := s.svc.DB.WithContext(receiptCtx).Model(&model.RunAction{}).Where("id = ? AND status = 'executing'", a.ID).Updates(map[string]any{"status": state, "result": result, "completed_at": now, "updated_at": now})
	if updated.Error != nil {
		return "", updated.Error
	}
	a.Status, a.Result, a.CompletedAt = state, result, &now
	if callErr != nil {
		return result, status.Error(codes.Unavailable, "tool outcome is not confirmed; action will not be replayed")
	}
	return result, nil
}

func grantView(g *model.ActionGrant) map[string]any {
	return map[string]any{"id": g.ID, "bot_id": g.BotID, "conv_id": g.ConvID, "server_id": g.ServerID, "tool_name": g.ToolName, "expires_at": g.ExpiresAt.Unix(), "revoked": g.RevokedAt != nil}
}

func (s *Service) ActionAuthorization(ctx context.Context, user, executorBot string, body []byte) ([]byte, error) {
	req, err := decodeActionRequest(body)
	if err != nil {
		return nil, err
	}
	executor := executorBot != ""
	if executor && req.Op != "request" && req.Op != "execute" && req.Op != "list" {
		return nil, status.Error(codes.PermissionDenied, "executor cannot approve actions")
	}
	if !executor && (req.Op == "request" || req.Op == "execute") {
		return nil, status.Error(codes.PermissionDenied, "action invocation requires authenticated executor")
	}
	if !executor && identity.Validate(user) != nil {
		return nil, status.Error(codes.Unauthenticated, "authenticated action principal required")
	}
	if !executor && (req.Op == "grant" || req.Op == "revoke" && req.GrantID != "" || req.Op == "list" && req.RunID == "") {
		return s.actionGrantAuthorization(ctx, user, req)
	}
	if !executor && req.RunID == "" && identity.Validate(req.ActionID) == nil && slices.Contains([]string{"share_confirmation", "approve", "deny", "revoke"}, req.Op) {
		var action model.RunAction
		if err := s.svc.DB.WithContext(ctx).Where("id = ?", req.ActionID).First(&action).Error; err != nil {
			return nil, err
		}
		req.RunID = action.RunID
	}
	if identity.Validate(req.RunID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid run_id required")
	}
	r, err := s.repo.Get(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	if executor {
		if r.ExecutorType != "third_party" || r.BotID != executorBot {
			return nil, status.Error(codes.PermissionDenied, "executor does not own Run")
		}
		if r.ExecutorStatus != "accepted" && r.ExecutorStatus != "stop_rejected" {
			return nil, status.Error(codes.FailedPrecondition, "executor must claim Run before resource actions")
		}
		if req.Generation != r.ExecutionGeneration || req.InputVersion != r.InputVersion {
			return nil, status.Error(codes.Aborted, "stale executor action")
		}
	} else if r.Visibility != "personal" || r.PersonalUserID != nil && *r.PersonalUserID == user {
		if err := s.visible(ctx, user, r); err != nil {
			return nil, err
		}
	} else if !slices.Contains([]string{"list", "approve", "deny", "revoke"}, req.Op) {
		return nil, status.Error(codes.PermissionDenied, "personal Run is not visible")
	}
	if req.Op == "list" {
		if !executor && r.Visibility == "personal" && (r.PersonalUserID == nil || *r.PersonalUserID != user) {
			actions, err := s.ownerActions(ctx, user, r.BotID, r.ConvID, r.ID)
			if err != nil {
				return nil, err
			}
			return json.Marshal(actions)
		}
		public := r.Public()
		s.decorateActions(ctx, &public, user)
		return json.Marshal(public.Actions)
	}
	if executor && (repo.TerminalRun(r) || r.Status == "stopping") {
		return nil, status.Error(codes.FailedPrecondition, "executor action authority ended")
	}
	if err := s.authorize(ctx, r, false); err != nil {
		return nil, err
	}
	if req.Op == "request" {
		frozen, _, err := s.actionTool(ctx, r, req.ServerID, req.ToolName)
		if err != nil {
			return nil, err
		}
		a, err := s.prepareAction(ctx, r, frozen, string(req.Arguments), req.SubmissionKey, req.ExpiresAt)
		if err != nil {
			return nil, err
		}
		return json.Marshal(s.publicAction(ctx, a, ""))
	}
	var a model.RunAction
	if identity.Validate(req.ActionID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid action_id required")
	}
	if err := s.svc.DB.WithContext(ctx).Where("id = ? AND run_id = ?", req.ActionID, r.ID).First(&a).Error; err != nil {
		return nil, err
	}
	if req.Generation != a.Generation || req.InputVersion != a.InputVersion {
		return nil, status.Error(codes.Aborted, "action version mismatch")
	}
	if req.ServerID != "" && req.ServerID != a.ServerID || req.ToolName != "" && req.ToolName != a.ToolName {
		return nil, status.Error(codes.PermissionDenied, "action tool differs from exact authorization")
	}
	if len(req.Arguments) > 0 {
		_, digest, err := canonicalActionArguments(req.Arguments, model.McpServer{})
		if err != nil {
			return nil, err
		}
		if digest != a.ParametersHash {
			return nil, status.Error(codes.PermissionDenied, "parameters differ from exact action authorization")
		}
	}
	if req.Op == "execute" {
		if req.SubmissionKey == "" {
			return nil, status.Error(codes.InvalidArgument, "execution submission key required")
		}
		frozen, _, err := s.actionTool(ctx, r, a.ServerID, a.ToolName)
		if err != nil {
			return nil, err
		}
		result, err := s.invokeAction(ctx, r, &a, frozen)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"action": s.publicAction(ctx, &a, ""), "result": result})
	}
	if req.Op == "share_confirmation" {
		_, err := s.repo.ChangeLifecycle(ctx, r.ID, a.Generation, func(tx *gorm.DB, current *model.Run) error {
			if current.PersonalUserID == nil || *current.PersonalUserID != user || current.RequesterID != user {
				return status.Error(codes.PermissionDenied, "only private requester may share confirmation")
			}
			if err := s.actionFence(tx, current, a.Generation, a.InputVersion); err != nil {
				return err
			}
			if err := s.privateConfirmationSources(tx, current); err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND run_id = ?", a.ID, current.ID).First(&a).Error; err != nil {
				return err
			}
			if a.Status != "pending" {
				return status.Error(codes.FailedPrecondition, "only pending confirmation may be shared")
			}
			if err := s.currentActionScope(tx, current, &a); err != nil {
				return err
			}
			var server model.McpServer
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND enabled = true", a.ServerID).First(&server).Error; err != nil {
				return err
			}
			if server.OwnerType != "user" || server.OwnerID == nil {
				return status.Error(codes.PermissionDenied, "confirmation requires a tool resource owner")
			}
			now := time.Now()
			a.ConfirmationSharedAt, a.ConfirmationOwnerID = &now, server.OwnerID
			return tx.Save(&a).Error
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(s.publicAction(ctx, &a, user))
	}
	if err := s.actionDisclosure(ctx, r, &a, user); err != nil {
		return nil, err
	}
	if !s.actionOwner(ctx, a.ServerID, user) {
		return nil, status.Error(codes.PermissionDenied, "only tool resource owner may authorize this action")
	}
	if !slices.Contains([]string{"approve", "deny", "revoke"}, req.Op) || req.SubmissionKey == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid action decision")
	}
	frozen, _, err := s.actionTool(ctx, r, a.ServerID, a.ToolName)
	if err != nil {
		return nil, err
	}
	_, err = s.repo.ChangeLifecycle(ctx, r.ID, a.Generation, func(tx *gorm.DB, current *model.Run) error {
		if err := s.actionFence(tx, current, a.Generation, a.InputVersion); err != nil {
			return err
		}
		server, err := currentActionTool(tx, current, frozen)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", a.ID).First(&a).Error; err != nil {
			return err
		}
		if _, _, err := repo.RunMember(tx, current.ConvID, user); err != nil {
			return err
		}
		if current.Visibility == "personal" && (current.PersonalUserID == nil || *current.PersonalUserID != user) {
			if a.ConfirmationSharedAt == nil || a.ConfirmationOwnerID == nil || *a.ConfirmationOwnerID != user {
				return status.Error(codes.PermissionDenied, "private confirmation not shared with this owner")
			}
			if err := s.privateConfirmationSources(tx, current); err != nil {
				return err
			}
		}
		if err := s.currentActionScope(tx, current, &a); err != nil {
			return err
		}
		if !actionPrincipalMatches(&a, current) {
			return status.Error(codes.PermissionDenied, "action approval principal changed")
		}
		if a.DefinitionHash != frozen.DefinitionHash {
			return status.Error(codes.PermissionDenied, "action definition changed")
		}
		_, digest, err := canonicalActionArguments(a.Arguments, server)
		if err != nil || digest != a.ParametersHash {
			return status.Error(codes.PermissionDenied, "action parameters changed")
		}
		if server.OwnerType != "user" || server.OwnerID == nil || *server.OwnerID != user {
			return status.Error(codes.PermissionDenied, "action resource ownership changed")
		}
		desired := map[string]string{"approve": "approved", "deny": "denied", "revoke": "revoked"}[req.Op]
		if a.Status == desired || req.Op == "approve" && slices.Contains([]string{"executing", "succeeded", "failed", "unknown"}, a.Status) {
			return nil
		}
		if a.Status != "pending" && !(req.Op == "revoke" && a.Status == "approved") {
			return status.Error(codes.FailedPrecondition, "action decision already final")
		}
		if req.Op == "approve" && !a.ExpiresAt.After(time.Now()) {
			return status.Error(codes.PermissionDenied, "action authorization expired")
		}
		if req.ExpiresAt != 0 {
			expiry := time.Unix(req.ExpiresAt, 0)
			if !expiry.After(time.Now()) || expiry.After(a.ExpiresAt) {
				return status.Error(codes.InvalidArgument, "approval cannot extend action lifetime")
			}
			a.ExpiresAt = expiry
		}
		a.Status, a.ApproverID = desired, user
		if err := tx.Save(&a).Error; err != nil {
			return err
		}
		if current.Status == "waiting_authorization" && current.Stage == "pending_action" {
			current.Status, current.Stage, current.WaitingReason = "running", "action_decided", ""
			if current.ExecutorType == "builtin" {
				next := *current
				next.Version++
				return repo.QueueRunExecution(tx, &next)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(s.publicAction(ctx, &a, user))
}

// Project the inbox after filtering current source and owner authority. This
// runs outside grant transactions: authorization may consult other services.
func (s *Service) ownerActions(ctx context.Context, user, botID, convID, runID string) ([]model.PublicAction, error) {
	if err := s.RequireMember(ctx, user, convID); err != nil {
		return nil, err
	}
	q := s.svc.DB.WithContext(ctx).Model(&model.RunAction{}).
		Joins("JOIN bot.runs inbox_run ON inbox_run.id = run_actions.run_id").
		Joins("JOIN bot.mcp_servers inbox_server ON inbox_server.id::text = run_actions.server_id").
		Where("run_actions.bot_id = ? AND inbox_run.conv_id = ? AND inbox_server.enabled = true AND inbox_server.owner_type = 'user' AND inbox_server.owner_id = ? AND run_actions.expires_at > ?", botID, convID, user, time.Now()).
		Where("inbox_run.visibility <> 'personal' OR (run_actions.confirmation_shared_at IS NOT NULL AND run_actions.confirmation_owner_id = ?) OR inbox_run.personal_user_id = ?", user, user)
	if runID != "" {
		q = q.Where("run_actions.run_id = ?", runID)
	}
	views := make([]model.PublicAction, 0, 100)
	for offset := 0; len(views) < 100; offset += 100 {
		var actions []model.RunAction
		if err := q.Select("run_actions.*").Order("run_actions.created_at DESC,run_actions.id DESC").Offset(offset).Limit(100).Find(&actions).Error; err != nil {
			return nil, err
		}
		for i := range actions {
			r, err := s.repo.Get(ctx, actions[i].RunID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return nil, err
			}
			if s.actionDisclosure(ctx, r, &actions[i], user) != nil {
				continue
			}
			views = append(views, s.publicAction(ctx, &actions[i], user))
			if len(views) == 100 {
				break
			}
		}
		if len(actions) < 100 {
			break
		}
	}
	return views, nil
}

func (s *Service) actionGrantAuthorization(ctx context.Context, user string, req actionRequest) ([]byte, error) {
	if req.Op == "revoke" {
		if identity.Validate(req.GrantID) != nil {
			return nil, status.Error(codes.InvalidArgument, "valid grant_id required")
		}
		var grant model.ActionGrant
		if err := s.svc.DB.WithContext(ctx).Where("id = ? AND owner_id = ?", req.GrantID, user).First(&grant).Error; err != nil {
			return nil, err
		}
		err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var server model.McpServer
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND enabled = true", grant.ServerID).First(&server).Error; err != nil {
				return err
			}
			if server.OwnerType != "user" || server.OwnerID == nil || *server.OwnerID != user {
				return status.Error(codes.PermissionDenied, "current resource owner required")
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", req.GrantID, user).First(&grant).Error; err != nil {
				return err
			}
			if grant.RevokedAt != nil {
				return nil
			}
			now := time.Now()
			grant.RevokedAt = &now
			return tx.Save(&grant).Error
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(grantView(&grant))
	}
	if identity.Validate(req.BotID) != nil || identity.Validate(req.ConvID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid bot and conversation required")
	}
	if err := s.RequireMember(ctx, user, req.ConvID); err != nil {
		return nil, err
	}
	if req.Op == "list" {
		var grants []model.ActionGrant
		if err := s.svc.DB.WithContext(ctx).Where("bot_id = ? AND conv_id = ? AND owner_id = ?", req.BotID, req.ConvID, user).Order("created_at,id").Find(&grants).Error; err != nil {
			return nil, err
		}
		views := make([]map[string]any, 0, len(grants))
		for i := range grants {
			views = append(views, grantView(&grants[i]))
		}
		actions, err := s.ownerActions(ctx, user, req.BotID, req.ConvID, "")
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"grants": views, "actions": actions})
	}
	if req.Op != "grant" || !s.actionOwner(ctx, req.ServerID, user) {
		return nil, status.Error(codes.PermissionDenied, "only tool resource owner may grant scoped action permission")
	}
	expiry := time.Unix(req.ExpiresAt, 0)
	if !expiry.After(time.Now()) || expiry.After(time.Now().Add(30*24*time.Hour)) {
		return nil, status.Error(codes.InvalidArgument, "action grant requires bounded expiry")
	}
	servers, err := s.svc.BotRepo.ListEnabledMcpServers(ctx, req.BotID)
	if err != nil {
		return nil, err
	}
	tools, err := component.DiscoverRunTools(ctx, servers)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(tools, func(t component.RunTool) bool { return t.ServerID == req.ServerID && t.Name == req.ToolName })
	if index < 0 || !actionWrite(tools[index].Risk) {
		return nil, status.Error(codes.InvalidArgument, "grant requires assigned concrete write tool")
	}
	frozen := tools[index]
	var grant model.ActionGrant
	err = s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding model.ConvBot
		var bot model.Bot
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("bot_id = ? AND conv_id = ? AND revoked_at IS NULL", req.BotID, req.ConvID).First(&binding).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", req.BotID).First(&bot).Error; err != nil {
			return err
		}
		scoped := &model.Run{BotID: req.BotID, ConvID: req.ConvID, BindingID: binding.ID, Visibility: "personal"}
		server, err := currentActionTool(tx, scoped, frozen)
		if err != nil {
			return err
		}
		if server.OwnerType != "user" || server.OwnerID == nil || *server.OwnerID != user {
			return status.Error(codes.PermissionDenied, "resource ownership changed")
		}
		_, digest, err := canonicalActionArguments(req.Arguments, server)
		if err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		// Exact parameters, binding and owner-requester: no delegation or broad wildcard.
		grant = model.ActionGrant{ID: id, BotID: req.BotID, ConvID: req.ConvID, BindingID: binding.ID, OwnerID: user, RequesterID: user, ServerID: req.ServerID, ToolName: req.ToolName, DefinitionHash: frozen.DefinitionHash, ParametersHash: digest, BotAuthorizationVersion: bot.AuthorizationVersion, BindingAuthorizationVersion: binding.AuthorizationVersion, ExpiresAt: expiry}
		member, _, err := repo.RunMember(tx, req.ConvID, user)
		if err != nil {
			return err
		}
		grant.SubjectMemberID = member
		return tx.Create(&grant).Error
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(grantView(&grant))
}

func (s *Service) actionInterrupt(ctx context.Context, r *model.Run, frozen component.RunTool, arguments, callID string) (*model.RunAction, error) {
	a, err := s.prepareAction(ctx, r, frozen, arguments, "tool:"+callID, 0)
	if err != nil {
		return nil, err
	}
	if a.Status == "pending" {
		return nil, compose.Interrupt(ctx, "concrete action approval required")
	}
	return a, nil
}
