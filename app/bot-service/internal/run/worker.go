package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/component"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	msg "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

var requestJSON = protojson.MarshalOptions{UseProtoNames: true}

// Start owns background execution independently of any HTTP/WS connection.
// Separate bounded pools prevent a slow model from blocking output or state delivery.
func (s *Service) Start(ctx context.Context) {
	for range 4 {
		go s.worker(ctx, []string{"execute"})
	}
	for range 2 {
		go s.worker(ctx, []string{"output"})
	}
	for range 4 {
		go s.worker(ctx, []string{"deliver"})
	}
	go s.worker(ctx, []string{"updated"})
	go s.monitorAuthority(ctx)
}
func (s *Service) worker(ctx context.Context, kinds []string) {
	for ctx.Err() == nil {
		item, err := s.repo.Claim(ctx, kinds)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				s.svc.Logger.Errorf("Run outbox claim failed: kind=%s", kinds[0])
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		workCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-workCtx.Done():
					return
				case <-time.After(20 * time.Second):
					if s.repo.Renew(workCtx, item) != nil {
						cancel()
						return
					}
				}
			}
		}()
		switch item.Kind {
		case "execute":
			err = s.execute(workCtx, item)
		case "output":
			err = s.publishOutput(workCtx, item)
		case "updated":
			err = s.publishUpdate(workCtx, item)
		case "deliver":
			err = s.deliverRun(workCtx, item)
		}
		cancel()
		<-done
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.svc.Logger.Errorf("Run outbox operation failed: kind=%s code=%s", item.Kind, status.Code(err).String())
			_ = s.repo.Retry(ctx, item)
		} else {
			_ = s.repo.Complete(ctx, item)
		}
	}
}
func (s *Service) fail(ctx context.Context, r *model.Run, generation int64, reason string, output bool) error {
	_, err := s.repo.ChangeLifecycle(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		current.Status = "failed"
		current.Stage = "failed"
		current.Error = reason
		if output || current.OutputStatus == "generated" || current.OutputStatus == "pending" {
			current.OutputStatus = "failed"
		}
		return tx.Model(&model.RunModelCall{}).Where("run_id = ? AND status = 'requested'", r.ID).Updates(map[string]any{"status": "failed", "completed_at": time.Now()}).Error
	})
	if status.Code(err) == codes.FailedPrecondition {
		return nil
	}
	return err
}
func (s *Service) execute(ctx context.Context, item *model.RunOutbox) error {
	r, err := s.repo.Get(ctx, item.RunID)
	if err != nil {
		return err
	}
	if repo.TerminalRun(r) || r.Status == "waiting_input" || r.Status == "waiting_authorization" {
		return nil
	}
	if r.Status == "stopping" {
		// A queued cancellation waits for any live execute lease. No model/tool
		// call remains by the time this item can confirm the local worker stopped.
		return s.confirmBuiltinStop(ctx, r, 0)
	}
	generation := item.ExecutionGeneration
	if generation <= 0 || r.ExecutionGeneration != generation {
		return status.Error(codes.Aborted, "execution lease superseded")
	}
	executionCtx, cancel := s.guardExecution(ctx, r, generation)
	err = s.advance(executionCtx, r, generation)
	cancel()
	current, lookupErr := s.repo.Get(ctx, r.ID)
	if lookupErr != nil {
		return lookupErr
	}
	current, lookupErr = s.reconcile(ctx, current)
	if lookupErr != nil {
		return lookupErr
	}
	if current.ExecutionGeneration != generation {
		return nil
	}
	if current.Status == "stopping" {
		return s.confirmBuiltinStop(ctx, current, generation)
	}
	if repo.TerminalRun(current) || current.Status == "waiting_authorization" {
		return nil
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || status.Code(err) == codes.Aborted {
		return err
	}
	reason := "execution failed"
	switch status.Code(err) {
	case codes.PermissionDenied, codes.Unauthenticated:
		reason = "authorization revoked or action not permitted"
	case codes.ResourceExhausted:
		reason = "execution budget exceeded"
	case codes.FailedPrecondition:
		reason = "execution configuration or source is no longer available"
	case codes.Unavailable, codes.DeadlineExceeded:
		reason = "model or tool service unavailable"
	}
	return s.fail(ctx, r, generation, reason, false)
}
func (s *Service) advance(ctx context.Context, r *model.Run, generation int64) error {
	if r.OutputStatus == "pending" {
		return nil
	}
	if r.OutputStatus == "generated" {
		return s.queueOutput(ctx, r, generation)
	}
	if err := s.authorize(ctx, r, false); err != nil {
		return err
	}
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return err
	}
	if config.AssemblyVersion != assemblyVersion {
		return status.Error(codes.FailedPrecondition, "unsupported request assembly version")
	}
	if r.TrajectoryVersion > 0 && r.Status == "accepted" {
		if _, err := s.repo.Change(ctx, r.ID, generation, func(_ *gorm.DB, current *model.Run) error {
			current.Status, current.ExecutorStatus = "running", "accepted"
			return nil
		}); err != nil {
			return err
		}
	}
	if r.TrajectoryVersion == 0 {
		_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
			if current.TrajectoryVersion != 0 {
				return status.Error(codes.Aborted, "trajectory already initialized")
			}
			current.Status = "running"
			current.Stage = "preparing"
			current.DeliveryStatus = "acknowledged"
			current.ExecutorStatus = "accepted"
			provenance := current.Provenance
			current.Provenance = json.RawMessage(`[]`)
			if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "system", Content: config.SystemPrompt}); err != nil {
				return err
			}
			current.Provenance = provenance
			return s.appendInputs(tx, current)
		})
		if err != nil {
			return err
		}
	}
	return s.runAgent(ctx, r, generation, config)
}

// appendInputs runs only at a complete model/tool boundary under the Run lock.
// A pending model request owns its frozen upper bound, even after more input
// has been accepted or a worker lease has been reclaimed.
func (s *Service) appendInputs(tx *gorm.DB, current *model.Run) error {
	aggregate := current.Provenance
	defer func() { current.Provenance = aggregate }()
	var pending int64
	if err := tx.Model(&model.RunModelCall{}).Where("run_id = ? AND status = 'requested'", current.ID).Count(&pending).Error; err != nil {
		return err
	}
	if pending != 0 {
		return nil
	}
	var inputs []model.RunInput
	if err := tx.Where("run_id = ? AND version > ? AND version <= ?", current.ID, current.AppliedInputVersion, current.InputVersion).Order("version ASC").Find(&inputs).Error; err != nil {
		return err
	}
	for _, input := range inputs {
		if input.Version != current.AppliedInputVersion+1 {
			return status.Error(codes.FailedPrecondition, "accepted input log is not continuous")
		}
		if input.Invalidated {
			current.AppliedInputVersion = input.Version
			continue
		}
		current.Provenance = input.Provenance
		if len(input.Materials) > 0 {
			var materials []Material
			if err := json.Unmarshal(input.Materials, &materials); err != nil {
				return err
			}
			if err := s.appendMaterials(tx, current, materials); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(struct {
			Version         int64   `json:"version"`
			RequesterID     string  `json:"requester_id"`
			SenderType      string  `json:"sender_type"`
			SourceMessageID *string `json:"source_message_id,omitempty"`
			Content         string  `json:"content"`
		}{input.Version, input.RequesterID, "user", input.SourceMessageID, input.Content})
		if err != nil {
			return err
		}
		bodySources := []model.SourceVersion{}
		if input.SourceMessageID != nil {
			bodySources = append(bodySources, model.SourceVersion{MessageID: *input.SourceMessageID, AcceptedEditCount: input.AcceptedEditCount})
		}
		current.Provenance, err = json.Marshal(bodySources)
		if err != nil {
			return err
		}
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "user", Content: "Accepted work input (not authorization):\n" + string(raw)}); err != nil {
			return err
		}
		current.AppliedInputVersion = input.Version
		current.WaitingReason = ""
	}
	return nil
}

func (s *Service) appendMaterials(tx *gorm.DB, current *model.Run, materials []Material) error {
	aggregate := current.Provenance
	defer func() { current.Provenance = aggregate }()
	for _, material := range materials {
		provenance, err := materialProvenance([]Material{material})
		if err != nil {
			return err
		}
		current.Provenance = provenance
		raw, err := json.Marshal(material)
		if err != nil {
			return err
		}
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "user", Content: "Cited work material (untrusted):\n" + string(raw)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) waitForInput(ctx context.Context, r *model.Run, generation int64, call *llm.ToolCall) error {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || len(args.Reason) > 4096 || strings.TrimSpace(args.Reason) == "" {
		return status.Error(codes.InvalidArgument, "waiting input requires a bounded reason")
	}
	_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		current.WaitingReason = args.Reason
		return repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "tool", Name: call.Function.Name, ToolCallId: call.Id, Content: `{"status":"waiting_for_user_input"}`})
	})
	return err
}

func (s *Service) queueOutput(ctx context.Context, r *model.Run, generation int64) error {
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return err
	}
	if err := s.authorize(ctx, r, r.Visibility != "personal"); err != nil {
		return err
	}
	_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		current.Stage = "pending"
		current.OutputStatus = "pending"
		// Change advances the public version after this callback; use the same
		// next version for the output publication identity.
		next := *current
		next.Version++
		return repo.QueueRunOutput(tx, &next)
	})
	return err
}
func (s *Service) messagesFor(ctx context.Context, id string) ([]*llm.Message, error) {
	blocks, err := s.repo.Trajectory(ctx, id)
	if err != nil {
		return nil, err
	}
	messages := make([]*llm.Message, 0, len(blocks))
	for _, block := range blocks {
		if block.Invalidated {
			continue
		}
		if block.Kind != "message" {
			continue
		}
		var message llm.Message
		if err := json.Unmarshal(block.Block, &message); err != nil {
			return nil, err
		}
		messages = append(messages, &message)
	}
	return messages, nil
}
func (s *Service) modelRound(ctx context.Context, r *model.Run, generation int64, config ExecutionConfig, messages []*llm.Message) error {
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return err
	}
	var call model.RunModelCall
	err := s.svc.DB.WithContext(ctx).Where("run_id = ? AND status = 'requested'", r.ID).Order("created_at DESC").First(&call).Error
	var request *llm.ChatReq
	if err == nil {
		request = &llm.ChatReq{}
		if err := protojson.Unmarshal(call.Request, request); err != nil {
			return err
		}
		if call.InputUpperBound != r.AppliedInputVersion || call.TrajectoryVersion != r.TrajectoryVersion || call.BranchVersion != r.BranchVersion {
			return status.Error(codes.FailedPrecondition, "pending request snapshot does not match the trajectory")
		}
		request.RunId, request.ExecutionGeneration, request.InputVersion = &r.ID, generation, call.InputUpperBound
	} else {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var calls int64
		if err := s.svc.DB.WithContext(ctx).Model(&model.RunModelCall{}).Where("run_id = ?", r.ID).Count(&calls).Error; err != nil {
			return err
		}
		if calls >= int64(config.MaxSteps) {
			return status.Error(codes.ResourceExhausted, "model round limit exceeded")
		}
		messages, err = s.workingModelView(ctx, r, generation, config)
		if err != nil {
			return err
		}
		request = &llm.ChatReq{ModelId: config.Model.ModelId, ExecutionModel: config.Model, OwnerId: config.OwnerID, BotId: &r.BotID, RunId: &r.ID, ExecutionGeneration: generation, InputVersion: r.AppliedInputVersion, Messages: messages, Temperature: config.Temperature, MaxTokens: config.MaxOutputTokens, CacheScope: config.CacheScope}
		for _, tool := range config.Tools {
			request.Tools = append(request.Tools, &llm.ToolDef{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
		}
		raw, err := requestJSON.Marshal(request)
		if err != nil {
			return err
		}
		cost, err := modelInputCost(messages, config)
		if err != nil {
			return err
		}
		if cost+workHeadroom(config) > config.MaxContextTokens {
			return status.Error(codes.ResourceExhausted, "compacted work view exceeds context budget")
		}
		checkpoint, err := s.currentCompaction(ctx, r, config)
		if err != nil {
			return err
		}
		var checkpointID *string
		if checkpoint != nil {
			checkpointID = &checkpoint.ID
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		call = model.RunModelCall{ID: id, RunID: r.ID, InteractionID: r.InteractionID, InputUpperBound: r.AppliedInputVersion, TrajectoryVersion: r.TrajectoryVersion, ExecutionGeneration: generation, BranchVersion: r.BranchVersion, SourceRevision: r.SourceRevision, CheckpointID: checkpointID, AssemblyVersion: assemblyVersion, Provenance: r.Provenance, Request: raw, Status: "requested"}
		if _, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
			if err := s.recheckAuthorizations(ctx, tx, current); err != nil {
				return err
			}
			var active model.RunCompactionCheckpoint
			checkpointErr := tx.Where("run_id = ? AND status = 'valid' AND branch_version = ? AND source_revision = ?", current.ID, current.BranchVersion, current.SourceRevision).Order("version DESC").First(&active).Error
			if checkpointErr != nil && !errors.Is(checkpointErr, gorm.ErrRecordNotFound) {
				return checkpointErr
			}
			if (call.CheckpointID == nil && checkpointErr == nil) || (call.CheckpointID != nil && (checkpointErr != nil || active.ID != *call.CheckpointID)) {
				return status.Error(codes.Aborted, "model compaction view changed before snapshot")
			}
			if current.TrajectoryVersion != call.TrajectoryVersion || current.AppliedInputVersion != call.InputUpperBound || current.BranchVersion != call.BranchVersion || current.SourceRevision != call.SourceRevision {
				return status.Error(codes.Aborted, "model trajectory changed before snapshot")
			}
			current.Stage = "model"
			return tx.Create(&call).Error
		}); err != nil {
			return err
		}
	}
	current, err := s.executionBoundary(ctx, r, generation)
	if err != nil {
		return err
	}
	r = current
	timeout := s.svc.Config.Bot.RequestTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	response, chunks, err := s.invokeModel(callCtx, request, config.Streaming)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if len(response.Choices) == 0 || response.Choices[0].Message == nil {
		return status.Error(codes.FailedPrecondition, "model returned no choice")
	}
	if len(response.Choices[0].Message.ToolCalls) > 16 {
		return status.Error(codes.ResourceExhausted, "too many tool calls in one model round")
	}
	output := response.Choices[0].Message
	if output.Role != "assistant" {
		return status.Error(codes.FailedPrecondition, "model response is not an assistant block")
	}
	ids := make(map[string]bool)
	for _, tool := range output.ToolCalls {
		if tool == nil || tool.Function == nil || tool.Id == "" || ids[tool.Id] {
			return status.Error(codes.FailedPrecondition, "invalid tool-call pairing")
		}
		ids[tool.Id] = true
		var server model.McpServer
		index := slices.IndexFunc(config.Tools, func(t component.RunTool) bool { return t.Name == tool.Function.Name })
		if index < 0 {
			return status.Error(codes.PermissionDenied, "unknown-risk tool cannot enter the work trajectory")
		}
		frozen := config.Tools[index]
		if frozen.ServerID != "builtin" && frozen.ServerID != "runtime" {
			if err := s.svc.DB.WithContext(ctx).Where("id = ?", frozen.ServerID).First(&server).Error; err != nil {
				return err
			}
		}
		if _, _, err := canonicalActionArguments([]byte(tool.Function.Arguments), server); err != nil {
			return err
		}
	}
	responseRaw, err := requestJSON.Marshal(response)
	if err != nil {
		return err
	}
	if len(chunks) > 0 {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(responseRaw, &body); err != nil {
			return err
		}
		body["stream_chunks"], err = json.Marshal(chunks)
		if err != nil {
			return err
		}
		responseRaw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	var usageRaw []byte
	if response.Usage != nil {
		usageRaw, err = requestJSON.Marshal(response.Usage)
		if err != nil {
			return err
		}
	}
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return err
	}
	now := time.Now()
	_, err = s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if err := s.recheckAuthorizations(ctx, tx, current); err != nil {
			return err
		}
		if current.BranchVersion != call.BranchVersion {
			return status.Error(codes.Aborted, "model response source view superseded")
		}
		result := tx.Model(&model.RunModelCall{}).Where("id = ? AND status = 'requested'", call.ID).Updates(map[string]any{"response": json.RawMessage(responseRaw), "usage": json.RawMessage(usageRaw), "first_token_latency_ms": response.FirstTokenLatencyMs, "completed_at": now, "status": "completed"})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return status.Error(codes.Aborted, "model response already recorded")
		}
		return repo.AppendRunBlock(tx, current, "message", output)
	})
	if err == nil {
		// Restricted diagnostics record versions and provider-reported numbers,
		// never prompt/tool text, credentials or high-cardinality metric labels.
		latency := "unreported"
		if response.FirstTokenLatencyMs != nil {
			latency = fmt.Sprintf("%d", *response.FirstTokenLatencyMs)
		}
		s.svc.Logger.Infof("Run model call recorded: assembly=%s input_version=%d trajectory_version=%d usage=%s first_token_latency_ms=%s", assemblyVersion, call.InputUpperBound, call.TrajectoryVersion, string(usageRaw), latency)
	}
	return err
}
func (s *Service) invokeModel(ctx context.Context, req *llm.ChatReq, streaming bool) (*llm.ChatResp, []json.RawMessage, error) {
	if !streaming {
		response, err := s.llm.ChatRequest(ctx, req)
		return response, nil, err
	}
	stream, err := s.llm.ChatRequestStream(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	chunks := make([]json.RawMessage, 0)
	rawBytes := 0
	response := &llm.ChatResp{}
	output := &llm.Message{Role: "assistant"}
	tools := make(map[int32]*llm.ToolCall)
	var indices []int32
	finish := ""
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, chunks, err
		}
		raw, err := requestJSON.Marshal(chunk)
		if err != nil {
			return nil, chunks, err
		}
		rawBytes += len(raw)
		if rawBytes > 2097152 {
			return nil, chunks, status.Error(codes.ResourceExhausted, "stream response exceeds work budget")
		}
		chunks = append(chunks, raw)
		if response.Id == "" {
			response.Id = chunk.Id
			response.Model = chunk.Model
		}
		if chunk.Usage != nil {
			response.Usage = chunk.Usage
		}
		if chunk.FirstTokenLatencyMs != nil {
			response.FirstTokenLatencyMs = chunk.FirstTokenLatencyMs
		}
		if len(output.Content)+len(output.ReasoningContent) > 1048576 || len(tools) > 16 {
			return nil, chunks, status.Error(codes.ResourceExhausted, "model response exceeds work budget")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
			delta := choice.Delta
			if delta == nil {
				continue
			}
			output.Content += delta.Content
			output.ReasoningContent += delta.ReasoningContent
			for _, part := range delta.ToolCalls {
				if part.Index == nil {
					return nil, chunks, status.Error(codes.FailedPrecondition, "stream tool-call delta has no index")
				}
				index := *part.Index
				if index < 0 || index >= 16 {
					return nil, chunks, status.Error(codes.ResourceExhausted, "stream tool index exceeds work budget")
				}
				current := tools[index]
				if current == nil {
					current = &llm.ToolCall{Function: &llm.ToolCall_Function{}}
					tools[index] = current
					indices = append(indices, index)
				}
				current.Id += part.Id
				if part.Type != "" {
					current.Type = part.Type
				}
				if part.Function != nil {
					current.Function.Name += part.Function.Name
					current.Function.Arguments += part.Function.Arguments
				}
				if len(current.Function.Arguments) > 65536 {
					return nil, chunks, status.Error(codes.ResourceExhausted, "tool arguments exceed work budget")
				}
			}
		}
	}
	slices.Sort(indices)
	for _, index := range indices {
		output.ToolCalls = append(output.ToolCalls, tools[index])
	}
	response.Choices = []*llm.ChatResp_Choice{{Index: 0, Message: output, FinishReason: finish}}
	return response, chunks, nil
}
func (s *Service) performTool(ctx context.Context, r *model.Run, generation int64, config ExecutionConfig, call *llm.ToolCall) error {
	current, err := s.executionBoundary(ctx, r, generation)
	if err != nil {
		return err
	}
	r = current
	index := slices.IndexFunc(config.Tools, func(t component.RunTool) bool { return t.Name == call.Function.Name })
	if index < 0 {
		return status.Error(codes.PermissionDenied, "external write or unknown-risk tool is not authorized")
	}
	frozen := config.Tools[index]
	if frozen.ServerID == "runtime" && frozen.Name == "ask_for_input" {
		return s.waitForInput(ctx, r, generation, call)
	}
	action, err := s.actionInterrupt(ctx, r, frozen, call.Function.Arguments, call.Id)
	if err != nil {
		return err
	}
	result, toolErr := s.invokeAction(ctx, r, action, frozen)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := s.executionBoundary(ctx, r, generation); err != nil {
		return err
	}
	if len(result) > 131072 {
		return status.Error(codes.ResourceExhausted, "tool result exceeds the work budget")
	}
	if toolErr != nil {
		// Errors become actual paired tool results rather than secret-bearing
		// transport errors or fallback answers. The private error is not public.
		if result == "" {
			result = `{"error":"tool invocation failed or no longer authorized"}`
		}
	}
	_, err = s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "tool", Name: call.Function.Name, ToolCallId: call.Id, Content: result}); err != nil {
			return err
		}
		if toolErr == nil && action.Status == "succeeded" {
			var sources []map[string]any
			if err := json.Unmarshal(current.Sources, &sources); err != nil {
				return err
			}
			sources = append(sources, map[string]any{"type": "tool", "tool_name": call.Function.Name})
			raw, err := json.Marshal(sources)
			if err != nil {
				return err
			}
			current.Sources = raw
		}
		return repo.AppendRunBlock(tx, current, "tool_roundtrip", map[string]any{"tool_call": call, "result": result, "failed": toolErr != nil, "executed": action.Status == "succeeded"})
	})
	if err != nil {
		return err
	}
	if toolErr != nil {
		return status.Error(codes.Unavailable, "tool execution failed")
	}
	return nil
}
func (s *Service) publishOutput(ctx context.Context, item *model.RunOutbox) error {
	r, err := s.repo.Get(ctx, item.RunID)
	if err != nil {
		return err
	}
	r, err = s.reconcile(ctx, r)
	if err != nil {
		return err
	}
	if err := s.refreshAuthorizations(ctx, r); err != nil {
		return err
	}
	if repo.TerminalRun(r) {
		return nil
	}
	if r.OutputStatus != "pending" {
		return status.Error(codes.FailedPrecondition, "output is not pending")
	}
	if err := s.authorize(ctx, r, r.Visibility != "personal"); err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.FailedPrecondition:
			return s.fail(ctx, r, 0, "output authorization revoked", true)
		default:
			return err
		}
	}
	if r.Visibility == "personal" {
		_, err = s.repo.ChangeLifecycle(ctx, r.ID, 0, func(tx *gorm.DB, current *model.Run) error {
			if err := s.recheckAuthorizations(ctx, tx, current); err != nil {
				return err
			}
			if err := s.actionFence(tx, current, current.ExecutionGeneration, current.InputVersion); err != nil {
				return err
			}
			if err := resourcegrant.CheckIDs(tx, resourceAccess(current), false, true); err != nil {
				return err
			}
			current.Status, current.Stage, current.OutputStatus = "succeeded", "committed", "committed"
			return nil
		})
		return err
	}
	var sources []map[string]any
	if err := json.Unmarshal(r.Sources, &sources); err != nil {
		return err
	}
	knowledgeSources := make([]map[string]any, 0)
	for _, source := range sources {
		if source["type"] == "rag" {
			knowledgeSources = append(knowledgeSources, source)
		}
	}
	blocks, err := s.repo.Trajectory(ctx, r.ID)
	if err != nil {
		return err
	}
	toolNames := make([]string, 0)
	for _, block := range blocks {
		if block.Kind != "tool_roundtrip" {
			continue
		}
		var roundtrip struct {
			ToolCall *llm.ToolCall `json:"tool_call"`
			Failed   bool          `json:"failed"`
			Executed bool          `json:"executed"`
		}
		if err := json.Unmarshal(block.Block, &roundtrip); err != nil {
			return err
		}
		if roundtrip.Executed && !roundtrip.Failed && roundtrip.ToolCall != nil && roundtrip.ToolCall.Function != nil {
			toolNames = append(toolNames, roundtrip.ToolCall.Function.Name)
		}
	}
	slices.Sort(toolNames)
	toolNames = slices.Compact(toolNames)
	raw, err := json.Marshal(map[string]any{"run_id": r.ID, "interaction_id": r.InteractionID, "kb_sources": knowledgeSources, "tool_names": toolNames})
	if err != nil {
		return err
	}
	key := r.ID + ":final"
	replyTo := r.SourceMessageID
	if replyTo == nil {
		replyTo = r.ReplyToMsgID
	}
	request := &msg.SendBotReplyReq{BotId: r.BotID, ConversationId: r.ConvID, BindingId: r.BindingID, RequesterId: &r.RequesterID, ControllerId: &r.ControllerID, Type: msg.MessageType_MESSAGE_TYPE_TEXT, Text: r.ResultText, ReplyToId: replyTo, RawPayload: string(raw), OutputKey: &key, RunId: &r.ID}
	switch r.ResultType {
	case "file":
		request.Type = msg.MessageType_MESSAGE_TYPE_FILE
		request.Text = ""
		request.File = &msg.FileContent{}
		if err := protocol.Unmarshal(r.ResultContent, request.File); err != nil {
			return err
		}
	case "result":
		request.Type = msg.MessageType_MESSAGE_TYPE_RESULT
		request.Text = ""
		request.Result = &msg.CustomContent{}
		if err := protocol.Unmarshal(r.ResultContent, request.Result); err != nil {
			return err
		}
	}
	response, err := s.messages.SendBotReply(serviceContext(ctx), request)
	if err != nil {
		code := status.Code(err)
		if code == codes.PermissionDenied || code == codes.Unauthenticated || code == codes.NotFound || code == codes.AlreadyExists || code == codes.FailedPrecondition || code == codes.InvalidArgument {
			return s.fail(ctx, r, 0, "final message submission failed", true)
		}
		return err
	}
	if identity.Validate(response.MessageId) != nil {
		return fmt.Errorf("messaging returned an invalid final message identity")
	}
	_, err = s.repo.ChangeLifecycle(ctx, r.ID, 0, func(_ *gorm.DB, current *model.Run) error {
		current.Status = "succeeded"
		current.Stage = "committed"
		current.OutputStatus = "committed"
		current.MessageID = &response.MessageId
		return nil
	})
	if status.Code(err) == codes.FailedPrecondition {
		return nil
	}
	return err
}
func (s *Service) publishUpdate(ctx context.Context, item *model.RunOutbox) error {
	r, err := s.repo.Get(ctx, item.RunID)
	if err != nil {
		return err
	}
	if r.Visibility == "personal" {
		return nil
	}
	var members []string
	if err := s.svc.DB.WithContext(ctx).Table("messaging.conv_members").Where("conv_id = ? AND member_type = 'user'", r.ConvID).Pluck("user_id", &members).Error; err != nil {
		return err
	}
	users := make([]string, 0, len(members))
	for _, user := range members {
		if s.visible(ctx, user, r) == nil {
			users = append(users, user)
		}
	}
	if len(users) == 0 {
		return nil
	}
	// WS is a lifecycle notification, not a viewer-specific material projection.
	// Rebuild via authenticated REST; delayed outbox bodies must not restore a
	// withdrawn source or a viewer's personally hidden derived result.
	public := r.Public()
	public.ResultText, public.ResultType, public.ResultContent = "", "", nil
	public.Sources, public.Actions = nil, nil
	public.WaitingReason, public.Error = "", ""
	raw, err := json.Marshal(map[string]any{"type": "bot.run.updated", "conv_id": r.ConvID, "bot_id": r.BotID, "run": public})
	if err != nil {
		return err
	}
	return s.svc.DeliveryPublisher.Publish(ctx, r.ConvID, delivery.Intent{UserIDs: users, Payload: raw})
}
