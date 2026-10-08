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
	_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
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
	if r.Status == "failed" || r.Status == "succeeded" || r.Status == "stopped" || r.Status == "waiting_input" {
		return nil
	}
	generation := item.ExecutionGeneration
	if generation <= 0 || r.ExecutionGeneration != generation {
		return status.Error(codes.Aborted, "execution lease superseded")
	}
	err = s.advance(ctx, r, generation)
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
	if r.TrajectoryVersion == 0 {
		_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
			if current.TrajectoryVersion != 0 {
				return status.Error(codes.Aborted, "trajectory already initialized")
			}
			current.Status = "running"
			current.Stage = "preparing"
			current.DeliveryStatus = "acknowledged"
			current.ExecutorStatus = "accepted"
			if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "system", Content: config.SystemPrompt}); err != nil {
				return err
			}
			return s.appendInputs(tx, current)
		})
		if err != nil {
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := s.repo.Get(ctx, r.ID)
		if err != nil {
			return err
		}
		r = current
		if r.Status == "waiting_input" {
			return nil
		}
		if r.ExecutionGeneration != generation {
			return status.Error(codes.Aborted, "execution superseded")
		}
		if err := s.authorize(ctx, r, false); err != nil {
			return err
		}
		messages, err := s.messagesFor(ctx, r.ID)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return status.Error(codes.FailedPrecondition, "missing work trajectory")
		}
		last := messages[len(messages)-1]
		// Resume any unfinished read-only tool roundtrip from the actual assistant
		// block; already persisted results are not reinvoked or reconstructed.
		assistantIndex := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				assistantIndex = i
				break
			}
		}
		if assistantIndex >= 0 && len(messages[assistantIndex].ToolCalls) > 0 {
			assistant := messages[assistantIndex]
			for _, call := range assistant.ToolCalls {
				if call == nil || call.Function == nil || call.Id == "" {
					return status.Error(codes.FailedPrecondition, "invalid model tool-call pairing")
				}
				if slices.ContainsFunc(messages[assistantIndex+1:], func(m *llm.Message) bool { return m.Role == "tool" && m.ToolCallId == call.Id }) {
					continue
				}
				if err := s.performTool(ctx, r, generation, config, call); err != nil {
					return err
				}
			}
			messages, err = s.messagesFor(ctx, r.ID)
			if err != nil {
				return err
			}
			r, err = s.repo.Get(ctx, r.ID)
			if err != nil {
				return err
			}
			last = messages[len(messages)-1]
			if r.WaitingReason != "" {
				r, err = s.repo.Change(ctx, r.ID, generation, func(_ *gorm.DB, current *model.Run) error {
					if current.InputVersion == current.AppliedInputVersion {
						current.Status = "waiting_input"
						current.Stage = "waiting_input"
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
			if r.Status == "waiting_input" {
				return nil
			}
		}
		if r.InputVersion > r.AppliedInputVersion {
			r, err = s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
				return s.appendInputs(tx, current)
			})
			if err != nil {
				return err
			}
			messages, err = s.messagesFor(ctx, r.ID)
			if err != nil {
				return err
			}
			last = messages[len(messages)-1]
		}
		if last.Role == "assistant" && len(last.ToolCalls) == 0 {
			if last.Content == "" {
				return status.Error(codes.FailedPrecondition, "model returned no final result")
			}
			if len(last.Content) > 262144 {
				return status.Error(codes.ResourceExhausted, "result too large")
			}
			r, err = s.repo.Change(ctx, r.ID, generation, func(_ *gorm.DB, current *model.Run) error {
				if current.InputVersion != current.AppliedInputVersion {
					return status.Error(codes.Aborted, "new input precedes completion")
				}
				current.Stage = "generated"
				current.ResultText = last.Content
				current.ResultType = "text"
				current.ExecutorStatus = "completed"
				current.OutputStatus = "generated"
				return nil
			})
			if status.Code(err) == codes.Aborted {
				continue
			}
			if err != nil {
				return err
			}
			return s.queueOutput(ctx, r, generation)
		}
		// modelRound creates its snapshot under the Run lock. Inputs accepted
		// afterwards belong to the next call, never to this immutable request.
		if err := s.modelRound(ctx, r, generation, config, messages); err != nil {
			return err
		}
	}
}

// appendInputs runs only at a complete model/tool boundary under the Run lock.
// A pending model request owns its frozen upper bound, even after more input
// has been accepted or a worker lease has been reclaimed.
func (s *Service) appendInputs(tx *gorm.DB, current *model.Run) error {
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
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "user", Content: "Accepted work input (not authorization):\n" + string(raw)}); err != nil {
			return err
		}
		current.AppliedInputVersion = input.Version
		current.WaitingReason = ""
	}
	return nil
}

func (s *Service) appendMaterials(tx *gorm.DB, current *model.Run, materials []Material) error {
	for _, material := range materials {
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
	if err := s.authorize(ctx, r, true); err != nil {
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
	var call model.RunModelCall
	err := s.svc.DB.WithContext(ctx).Where("run_id = ? AND status = 'requested'", r.ID).Order("created_at DESC").First(&call).Error
	var request *llm.ChatReq
	if err == nil {
		request = &llm.ChatReq{}
		if err := protojson.Unmarshal(call.Request, request); err != nil {
			return err
		}
		if call.InputUpperBound != r.AppliedInputVersion || call.TrajectoryVersion != r.TrajectoryVersion {
			return status.Error(codes.FailedPrecondition, "pending request snapshot does not match the trajectory")
		}
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
		request = &llm.ChatReq{ModelId: config.Model.ModelId, ExecutionModel: config.Model, OwnerId: config.OwnerID, BotId: &r.BotID, Messages: messages, Temperature: config.Temperature, MaxTokens: config.MaxOutputTokens, CacheScope: config.CacheScope}
		for _, tool := range config.Tools {
			request.Tools = append(request.Tools, &llm.ToolDef{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
		}
		raw, err := requestJSON.Marshal(request)
		if err != nil {
			return err
		}
		// Conservative bound including tools, materials and output headroom. No
		// partial history truncation can create unmatched tool/result pairs.
		if len(raw)/2+int(config.MaxOutputTokens) > config.MaxContextTokens {
			return status.Error(codes.ResourceExhausted, "context budget exceeded; start a smaller work request")
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		call = model.RunModelCall{ID: id, RunID: r.ID, InteractionID: r.InteractionID, InputUpperBound: r.AppliedInputVersion, TrajectoryVersion: r.TrajectoryVersion, ExecutionGeneration: generation, BranchVersion: 1, AssemblyVersion: assemblyVersion, Request: raw, Status: "requested"}
		if _, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
			if current.TrajectoryVersion != call.TrajectoryVersion || current.AppliedInputVersion != call.InputUpperBound {
				return status.Error(codes.Aborted, "model trajectory changed before snapshot")
			}
			current.Stage = "model"
			return tx.Create(&call).Error
		}); err != nil {
			return err
		}
	}
	if err := s.authorize(ctx, r, false); err != nil {
		return err
	}
	timeout := s.svc.Config.Bot.RequestTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	response, chunks, err := s.invokeModel(callCtx, request, config.Streaming)
	cancel()
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
	now := time.Now()
	_, err = s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
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
	if err := s.authorize(ctx, r, false); err != nil {
		return err
	}
	index := slices.IndexFunc(config.Tools, func(t component.RunTool) bool { return t.Name == call.Function.Name })
	if index < 0 {
		return status.Error(codes.PermissionDenied, "external write or unknown-risk tool is not authorized")
	}
	frozen := config.Tools[index]
	if frozen.ServerID == "runtime" && frozen.Name == "ask_for_input" {
		return s.waitForInput(ctx, r, generation, call)
	}
	var server model.McpServer
	if frozen.ServerID != "builtin" {
		servers, err := s.svc.BotRepo.ListEnabledMcpServers(ctx, r.BotID)
		if err != nil {
			return err
		}
		serverIndex := slices.IndexFunc(servers, func(server model.McpServer) bool { return server.ID == frozen.ServerID })
		if serverIndex < 0 {
			return status.Error(codes.PermissionDenied, "tool access revoked")
		}
		server = servers[serverIndex]
	}
	if _, err := s.repo.Change(ctx, r.ID, generation, func(_ *gorm.DB, current *model.Run) error { current.Stage = "tool"; return nil }); err != nil {
		return err
	}
	toolCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var result string
	var toolErr error
	if frozen.ServerID == "builtin" {
		result, toolErr = component.CallBuiltinRunTool(toolCtx, frozen, call.Function.Arguments)
	} else {
		result, toolErr = component.CallRunTool(toolCtx, server, frozen, call.Function.Arguments)
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
	_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "tool", Name: call.Function.Name, ToolCallId: call.Id, Content: result}); err != nil {
			return err
		}
		if toolErr == nil {
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
		return repo.AppendRunBlock(tx, current, "tool_roundtrip", map[string]any{"tool_call": call, "result": result, "failed": toolErr != nil})
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
	if r.Status == "succeeded" || r.Status == "failed" {
		return nil
	}
	if r.OutputStatus != "pending" {
		return status.Error(codes.FailedPrecondition, "output is not pending")
	}
	if err := s.authorize(ctx, r, true); err != nil {
		switch status.Code(err) {
		case codes.PermissionDenied, codes.Unauthenticated, codes.NotFound, codes.FailedPrecondition:
			return s.fail(ctx, r, 0, "output authorization revoked", true)
		default:
			return err
		}
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
		}
		if err := json.Unmarshal(block.Block, &roundtrip); err != nil {
			return err
		}
		if !roundtrip.Failed && roundtrip.ToolCall != nil && roundtrip.ToolCall.Function != nil {
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
	request := &msg.SendBotReplyReq{BotId: r.BotID, ConversationId: r.ConvID, BindingId: r.BindingID, RequesterId: &r.RequesterID, Type: msg.MessageType_MESSAGE_TYPE_TEXT, Text: r.ResultText, ReplyToId: replyTo, RawPayload: string(raw), OutputKey: &key}
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
	_, err = s.repo.Change(ctx, r.ID, 0, func(_ *gorm.DB, current *model.Run) error {
		current.Status = "succeeded"
		current.Stage = "committed"
		current.OutputStatus = "committed"
		current.MessageID = &response.MessageId
		return nil
	})
	return err
}
func (s *Service) publishUpdate(ctx context.Context, item *model.RunOutbox) error {
	r, err := s.repo.Get(ctx, item.RunID)
	if err != nil {
		return err
	}
	members, err := s.members(ctx, r.ConvID, r.RequesterID)
	if err != nil {
		if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.NotFound || status.Code(err) == codes.Unauthenticated {
			return nil
		}
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
	// Old event versions stay immutable; recipients restore missed changes via
	// authoritative query, never via token streams or inbox sequence positions.
	return s.svc.DeliveryPublisher.Publish(ctx, r.ConvID, delivery.Intent{UserIDs: users, Payload: item.Payload})
}
