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
	if identity.Validate(user) != nil || identity.Validate(serverID) != nil {
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
	if a.SourceInvalidated {
		return model.PublicAction{ID: a.ID, RunID: a.RunID, ToolName: a.ToolName, ServerID: a.ServerID,
			Risk: a.Risk, Status: state, Target: "[redacted]", Parameters: json.RawMessage(`{"redacted":true}`),
			ParametersJSON: `{"redacted":true}`, ExpiresAt: a.ExpiresAt.Unix(), Generation: a.Generation,
			InputVersion: a.InputVersion, ConfirmationShared: a.ConfirmationSharedAt != nil}
	}
	r, err := s.repo.Get(ctx, a.RunID)
	owner := s.actionOwner(ctx, a.ServerID, user)
	sourceValid := err == nil && s.actionSourcesCurrent(s.svc.DB.WithContext(ctx), r, a) == nil
	runtimeReader := sourceValid && a.ServerID == "runtime" && isRuntimeReadTool(a.ToolName) && s.visible(ctx, user, r) == nil && s.authorize(ctx, r, r.Visibility != "personal") == nil
	personalRequester := sourceValid && r.Visibility == "personal" && r.PersonalUserID != nil && *r.PersonalUserID == user && s.visible(ctx, user, r) == nil
	valid := err == nil && s.actionDisclosure(ctx, r, a, user) == nil
	can := owner && valid && state == "pending" && !repo.TerminalRun(r) && r.Status != "stopping"
	parameters := json.RawMessage(`{"redacted":true}`)
	target := "[redacted]"
	if personalRequester || owner && valid || runtimeReader {
		var value any
		dec := json.NewDecoder(bytes.NewReader(a.Arguments))
		dec.UseNumber()
		if dec.Decode(&value) == nil {
			parameters, _ = json.Marshal(redactActionValue(value))
		}
		target = a.Target
	}
	result := ""
	if (personalRequester || owner && valid && r.Visibility != "personal" || runtimeReader) && slices.Contains([]string{"succeeded", "failed", "unknown", "denied", "revoked"}, state) {
		result = a.Result
		if !runtimeReader {
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
	if err := s.actionDisclosureAccess(ctx, r, a, user); err != nil {
		return err
	}
	return s.currentActionScope(s.svc.DB.WithContext(ctx), r, a)
}

// Receipt replay requires current disclosure rights, not a renewed permit.
func (s *Service) actionDisclosureAccess(ctx context.Context, r *model.Run, a *model.RunAction, user string) error {
	tx := s.svc.DB.WithContext(ctx)
	if err := s.actionSourcesCurrent(tx, r, a); err != nil {
		return err
	}
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
	return actionDefinitionCurrent(tx, r, a)
}

func (s *Service) currentActionScope(tx *gorm.DB, r *model.Run, a *model.RunAction) error {
	if err := s.actionSourcesCurrent(tx, r, a); err != nil {
		return err
	}
	if r.AccessRevoked || r.Status == "stopping" || a.RunID != r.ID || a.BotID != r.BotID || a.BindingID != r.BindingID || a.RequesterID != r.RequesterID || a.Generation != r.ExecutionGeneration || a.InputVersion != r.InputVersion || !actionPrincipalMatches(a, r) || !a.ExpiresAt.After(time.Now()) {
		return status.Error(codes.PermissionDenied, "action scope is no longer current")
	}
	return actionDefinitionCurrent(tx, r, a)
}

func actionDefinitionCurrent(tx *gorm.DB, r *model.Run, a *model.RunAction) error {
	var frozen component.RunTool
	if json.Unmarshal(a.ToolDefinition, &frozen) != nil || frozen.ServerID != a.ServerID || frozen.Name != a.ToolName || frozen.DefinitionHash != a.DefinitionHash || frozen.Risk != a.Risk {
		return status.Error(codes.PermissionDenied, "action definition changed")
	}
	server, err := currentActionTool(tx, r, frozen)
	if err != nil {
		return err
	}
	_, digest, err := canonicalToolActionArguments(a.Arguments, server, frozen)
	if err != nil || digest != a.ParametersHash {
		return status.Error(codes.PermissionDenied, "action parameters changed")
	}
	return nil
}

// Source validity belongs to the action's frozen inputs, not the Run's current
// result flag. A resumed Run can accept new work without reviving an old action.
func (s *Service) actionSourcesCurrent(tx *gorm.DB, r *model.Run, a *model.RunAction) error {
	if a.SourceInvalidated {
		return status.Error(codes.PermissionDenied, "action source is no longer available")
	}
	frozen := *r
	frozen.Provenance = a.Provenance
	return repo.CheckRunSources(tx, &frozen)
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
func (s *Service) actionAuthorityFence(tx *gorm.DB, r *model.Run, generation, input int64) error {
	if repo.TerminalRun(r) || r.Status == "stopping" || r.AccessRevoked || r.ExecutionGeneration != generation || r.InputVersion != input {
		return status.Error(codes.Aborted, "action execution authority superseded")
	}
	if r.Status == "waiting_authorization" && r.Stage != "pending_action" {
		return status.Error(codes.FailedPrecondition, "Run requires explicit takeover")
	}
	if err := unresolvedWriteAction(tx, r); err != nil {
		return err
	}
	return s.actionParticipationFence(tx, r)
}

func (s *Service) actionParticipationFence(tx *gorm.DB, r *model.Run) error {
	if r.AccessRevoked {
		return status.Error(codes.PermissionDenied, "Run authority revoked")
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
	return repo.CheckRunSources(tx, r)
}

func (s *Service) actionFence(tx *gorm.DB, r *model.Run, generation, input int64) error {
	if err := s.actionAuthorityFence(tx, r, generation, input); err != nil {
		return err
	}
	return repo.CheckExecutionLease(tx, r, generation)
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
	if frozen.ServerID == "runtime" {
		if !isRuntimeReadTool(frozen.Name) || frozen.Risk != "read" || !frozen.CanShare {
			return server, status.Error(codes.PermissionDenied, "unknown runtime action")
		}
		var bot model.Bot
		query := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND status = 'active'", r.BotID)
		if frozen.Name != "read_conversation_history" {
			query = query.Where("enable_knowledge = true")
		}
		if err := query.Take(&bot).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return server, err
			}
			return server, status.Error(codes.PermissionDenied, "knowledge capability revoked")
		}
		ref := resourcegrant.ToolRef{ServerID: frozen.ServerID, Name: frozen.Name, DefinitionHash: frozen.DefinitionHash, Risk: frozen.Risk, CanShare: frozen.CanShare}
		return server, resourcegrant.CheckToolPermissions(tx, resourceAccess(r), []resourcegrant.ToolRef{ref}, true)
	}
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
		canonical, digest, err := canonicalToolActionArguments([]byte(arguments), server, frozen)
		if err != nil {
			return err
		}
		priorErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ? AND submission_key = ?", r.ID, submission).First(&result).Error
		if priorErr == nil {
			return s.rebindAction(tx, current, &result, frozen, server, digest)
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
		result.Provenance = current.Provenance
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
			if r.RequesterID != "" {
				if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("bot_id = ? AND binding_id = ? AND requester_id = ? AND server_id = ? AND tool_name = ? AND definition_hash = ? AND parameters_hash = ? AND revoked_at IS NULL AND expires_at > NOW() AND bot_authorization_version = ? AND binding_authorization_version = ?", r.BotID, r.BindingID, r.RequesterID, frozen.ServerID, frozen.Name, frozen.DefinitionHash, digest, r.BotAuthorizationVersion, r.BindingAuthorizationVersion).Find(&grants).Error; err != nil {
					return err
				}
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
		if err := s.actionFence(tx, current, r.ExecutionGeneration, r.InputVersion); err != nil {
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
		if err := s.rebindAction(tx, current, a, frozen, server, a.ParametersHash); err != nil {
			return err
		}
		switch a.Status {
		case "succeeded", "failed":
			replay = true
			return nil
		case "executing", "unknown":
			return status.Error(codes.FailedPrecondition, "action outcome unknown; external write will not be replayed")
		case "expired":
			return status.Error(codes.PermissionDenied, "action authorization expired")
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
			if err := actionApprovalCurrent(tx, current, a, server); err != nil {
				return err
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
	var knowledge *knowledgeSearchOutcome
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
			if err := s.actionSourcesCurrent(tx, current, &live); err != nil {
				return err
			}
			if !actionPrincipalMatches(&live, current) || !live.ExpiresAt.After(time.Now()) || live.ParametersHash != a.ParametersHash || live.Generation != current.ExecutionGeneration || live.InputVersion != current.InputVersion {
				return status.Error(codes.PermissionDenied, "claimed action scope superseded")
			}
			if actionWrite(live.Risk) {
				if err := actionApprovalCurrent(tx, current, &live, liveServer); err != nil {
					return err
				}
			}
			return nil
		})
		return err
	}
	if frozen.ServerID == "runtime" && isRuntimeReadTool(frozen.Name) {
		callErr = beforeCall(toolCtx)
		if callErr == nil {
			switch frozen.Name {
			case "list_knowledge_bases":
				knowledge, callErr = s.executeKnowledgeList(toolCtx, r, string(a.Arguments))
			case "list_knowledge_documents":
				knowledge, callErr = s.executeDocumentList(toolCtx, r, string(a.Arguments))
			case "read_knowledge_document":
				knowledge, callErr = s.executeDocumentRead(toolCtx, r, string(a.Arguments))
			case "read_conversation_history":
				knowledge, callErr = s.executeHistoryRead(toolCtx, r, string(a.Arguments))
			default:
				knowledge, callErr = s.executeKnowledgeSearch(toolCtx, r, string(a.Arguments))
			}
			if knowledge != nil {
				result = knowledge.Result
			}
		}
	} else if frozen.ServerID == "builtin" {
		callErr = beforeCall(toolCtx)
		if callErr == nil {
			result, callErr = component.CallBuiltinRunTool(toolCtx, frozen, string(a.Arguments))
		}
	} else {
		result, callErr = component.CallRunTool(toolCtx, server, frozen, string(a.Arguments), beforeCall)
	}
	state := "succeeded"
	if knowledge != nil && knowledge.Failed {
		state = "failed"
	}
	if callErr != nil {
		state = "unknown"
		if a.Risk == "read" || a.Risk == "draft" {
			state = "failed"
		}
		result = `{"status":"` + state + `","error":"tool invocation did not produce a confirmed success"}`
	}
	if len(result) > 131072 {
		result = `{"status":"` + state + `","result":"omitted: tool result exceeded work budget"}`
	}
	var decoded any
	if knowledge == nil && json.Unmarshal([]byte(result), &decoded) == nil {
		raw, _ := json.Marshal(redactActionValue(decoded))
		result = string(raw)
	}
	// Stop/revocation may hide a fact; it never authorizes an old executor receipt.
	receiptCtx, receiptCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer receiptCancel()
	_, receiptErr := s.repo.ChangeActionFacts(receiptCtx, r.ID, func(tx *gorm.DB, current *model.Run) error {
		// Stop and source revocation may hide the payload, but only the current
		// live claim can record a fact. An old worker cannot erase an unknown.
		if err := repo.CheckExecutionLease(tx, current, a.Generation); err != nil {
			return err
		}
		var live model.RunAction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND run_id = ?", a.ID, current.ID).First(&live).Error; err != nil {
			return err
		}
		if live.Status != "executing" || live.Generation != a.Generation || live.InputVersion != a.InputVersion || live.ParametersHash != a.ParametersHash || live.DefinitionHash != a.DefinitionHash {
			return status.Error(codes.Aborted, "action receipt claim superseded")
		}
		now := time.Now()
		live.Status, live.CompletedAt = state, &now
		if !live.SourceInvalidated {
			if err := s.acceptKnowledgeOutcome(tx, current, &live, knowledge); err != nil {
				return err
			}
			live.Result = result
		}
		if err := tx.Save(&live).Error; err != nil {
			return err
		}
		*a = live
		if state == "unknown" && !repo.TerminalRun(current) && current.Status != "stopping" {
			current.Status, current.Stage, current.WaitingReason = "waiting_authorization", "action_outcome_unknown", "action_outcome_unknown"
			current.StreamStatus = "closed"
		}
		return nil
	})
	if receiptErr != nil {
		return "", receiptErr
	}
	if err := s.refreshAuthorizations(receiptCtx, r); err != nil {
		return "", err
	}
	if err := s.svc.DB.WithContext(receiptCtx).Where("id = ?", a.ID).First(a).Error; err != nil {
		return "", err
	}
	if a.SourceInvalidated {
		return "", status.Error(codes.PermissionDenied, "action source is no longer available")
	}
	if state == "unknown" {
		return result, status.Error(codes.FailedPrecondition, "action outcome unknown; human reconciliation required")
	}
	if callErr != nil {
		if frozen.ServerID == "runtime" && isRuntimeReadTool(frozen.Name) {
			return "", callErr
		}
		return result, status.Error(codes.Unavailable, "tool invocation failed")
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
	// Reconcile outside authority transactions: source checks may call other
	// domains. The transaction fences below recheck durable source state.
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return nil, err
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
	decision := !executor && slices.Contains([]string{"approve", "deny", "revoke"}, req.Op)
	if !decision && (req.Generation != a.Generation || req.InputVersion != a.InputVersion) {
		return nil, status.Error(codes.Aborted, "action version mismatch")
	}
	if !decision && (req.ServerID != "" && req.ServerID != a.ServerID || req.ToolName != "" && req.ToolName != a.ToolName) {
		return nil, status.Error(codes.PermissionDenied, "action tool differs from exact authorization")
	}
	if !decision && len(req.Arguments) > 0 {
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
		_, err := s.repo.ChangeLifecycle(ctx, r.ID, 0, func(tx *gorm.DB, current *model.Run) error {
			if current.PersonalUserID == nil || *current.PersonalUserID != user || current.RequesterID != user {
				return status.Error(codes.PermissionDenied, "only private requester may share confirmation")
			}
			if err := s.actionAuthorityFence(tx, current, a.Generation, a.InputVersion); err != nil {
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
	if err := s.actionDisclosureAccess(ctx, r, &a, user); err != nil {
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
	err = s.actionDecision(ctx, r, &a, user, req, func(tx *gorm.DB, current *model.Run) error {
		if err := s.actionAuthorityFence(tx, current, a.Generation, a.InputVersion); err != nil {
			return err
		}
		server, err := currentActionTool(tx, current, frozen)
		if err != nil {
			return err
		}
		if req.Generation != a.Generation || req.InputVersion != a.InputVersion {
			return status.Error(codes.Aborted, "action version mismatch")
		}
		if req.ServerID != "" && req.ServerID != a.ServerID || req.ToolName != "" && req.ToolName != a.ToolName {
			return status.Error(codes.PermissionDenied, "action tool differs from exact authorization")
		}
		if len(req.Arguments) > 0 {
			_, digest, err := canonicalActionArguments(req.Arguments, server)
			if err != nil {
				return err
			}
			if digest != a.ParametersHash {
				return status.Error(codes.PermissionDenied, "parameters differ from exact action authorization")
			}
		}
		if err := s.currentActionScope(tx, current, &a); err != nil {
			return err
		}
		if a.DefinitionHash != frozen.DefinitionHash {
			return status.Error(codes.PermissionDenied, "action definition changed")
		}
		desired := map[string]string{"approve": "approved", "deny": "denied", "revoke": "revoked"}[req.Op]
		if a.Status == desired || req.Op == "approve" && slices.Contains([]string{"executing", "succeeded", "failed", "unknown"}, a.Status) {
			return nil
		}
		if a.Status != "pending" && !(req.Op == "revoke" && a.Status == "approved") {
			return status.Error(codes.FailedPrecondition, "action decision already final")
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
			if err := s.refreshAuthorizations(ctx, r); err != nil {
				return nil, err
			}
			if err := s.svc.DB.WithContext(ctx).Where("id = ?", actions[i].ID).First(&actions[i]).Error; err != nil {
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

var errActionDecisionChanged = errors.New("action changed during decision authorization")

// Human decisions are durable commands. Replays reauthorize disclosure and
// return the current action, without renewing or applying an old approval.
func (s *Service) actionDecision(ctx context.Context, r *model.Run, a *model.RunAction, user string, req actionRequest, apply func(*gorm.DB, *model.Run) error) error {
	digest, err := hash(req)
	if err != nil {
		return err
	}
	key := "action-decision:" + user + ":" + req.SubmissionKey
	for {
		checked, err := s.repo.Get(ctx, r.ID)
		if err != nil {
			return err
		}
		if err := s.svc.DB.WithContext(ctx).Where("id = ? AND run_id = ?", a.ID, checked.ID).First(a).Error; err != nil {
			return err
		}
		if err := s.actionDisclosureAccess(ctx, checked, a, user); err != nil {
			return err
		}
		if !s.actionOwner(ctx, a.ServerID, user) {
			return status.Error(codes.PermissionDenied, "only current tool owner may replay an action decision")
		}
		err = s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := repo.LockRunConversation(tx, checked.ConvID); err != nil {
				return err
			}
			if err := repo.LockDelegationTree(tx, checked); err != nil {
				return err
			}
			var interaction model.Interaction
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", checked.InteractionID).First(&interaction).Error; err != nil {
				return err
			}
			var current model.Run
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", checked.ID).First(&current).Error; err != nil {
				return err
			}
			if current.Version != checked.Version {
				return errActionDecisionChanged
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND run_id = ?", a.ID, current.ID).First(a).Error; err != nil {
				return err
			}
			if err := s.actionParticipationFence(tx, &current); err != nil {
				return err
			}
			if err := s.actionSourcesCurrent(tx, &current, a); err != nil {
				return err
			}
			if err := actionDefinitionCurrent(tx, &current, a); err != nil {
				return err
			}
			if _, _, err := repo.RunMember(tx, current.ConvID, user); err != nil {
				return err
			}
			if current.Visibility == "personal" && (current.PersonalUserID == nil || *current.PersonalUserID != user) {
				if a.ConfirmationSharedAt == nil || a.ConfirmationOwnerID == nil || *a.ConfirmationOwnerID != user {
					return status.Error(codes.PermissionDenied, "private confirmation no longer shared with owner")
				}
				if err := s.privateConfirmationSources(tx, &current); err != nil {
					return err
				}
			}
			var server model.McpServer
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND enabled = true", a.ServerID).First(&server).Error; err != nil {
				return err
			}
			if server.OwnerType != "user" || server.OwnerID == nil || *server.OwnerID != user {
				return status.Error(codes.PermissionDenied, "action resource ownership changed")
			}
			var prior model.RunReceipt
			priorErr := tx.Where("run_id = ? AND update_key = ?", current.ID, key).First(&prior).Error
			if priorErr == nil {
				if prior.ContentHash != digest {
					return status.Error(codes.AlreadyExists, "action decision key has different content")
				}
				return nil
			}
			if !errors.Is(priorErr, gorm.ErrRecordNotFound) {
				return priorErr
			}
			if repo.TerminalRun(&current) || interaction.Status != "active" {
				return status.Error(codes.FailedPrecondition, "Run no longer accepts new action decisions")
			}
			before := current.Stage
			if err := apply(tx, &current); err != nil {
				return err
			}
			current.Version++
			current.UpdatedAt = time.Now()
			if current.Stage != before {
				current.Steps = append(current.Steps, model.RunStep{Version: current.Version, Stage: current.Stage, CreatedAt: current.UpdatedAt.Unix()})
			}
			if err := tx.Save(&current).Error; err != nil {
				return err
			}
			if err := repo.QueueRunUpdated(tx, &current); err != nil {
				return err
			}
			id, err := identity.New()
			if err != nil {
				return err
			}
			raw, err := json.Marshal(map[string]string{"action_id": a.ID, "status": a.Status})
			if err != nil {
				return err
			}
			return tx.Create(&model.RunReceipt{ID: id, RunID: current.ID, UpdateKey: key, ContentHash: digest, Receipt: raw}).Error
		})
		if !errors.Is(err, errActionDecisionChanged) {
			return err
		}
	}
}
