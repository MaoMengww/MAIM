package run

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/maomeng/aim/app/bot-service/internal/component"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// The bridge owns no business history. Every model/tool boundary uses AIM's
// durable trajectory, current authority and claim, including after ADK resume.
type runAgentBridge struct {
	*adk.BaseChatModelAgentMiddleware
	service    *Service
	run        *model.Run
	generation int64
	config     ExecutionConfig
	replay     *llm.Message
	messages   []*llm.Message
	callMu     sync.Mutex
}

func agentMessage(m *llm.Message) *schema.Message {
	out := &schema.Message{Role: schema.RoleType(m.Role), Content: m.Content, ReasoningContent: m.ReasoningContent, Name: m.Name, ToolCallID: m.ToolCallId, ToolName: m.Name}
	for _, call := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{ID: call.Id, Type: call.Type, Function: schema.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}})
	}
	return out
}

func (b *runAgentBridge) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	r, err := b.service.executionBoundary(ctx, b.run, b.generation)
	if err == nil {
		err = b.service.refreshSources(ctx, r)
	}
	if err != nil {
		return ctx, state, err
	}
	messages, err := b.service.messagesFor(ctx, r.ID)
	if err != nil {
		return ctx, state, err
	}
	if len(messages) == 0 {
		return ctx, state, status.Error(codes.FailedPrecondition, "missing work trajectory")
	}
	var failedTools int64
	if err := b.service.svc.DB.WithContext(ctx).Model(&model.RunTrajectory{}).Where("run_id = ? AND invalidated = false AND kind = 'tool_roundtrip' AND block->>'failed' = 'true'", r.ID).Count(&failedTools).Error; err != nil {
		return ctx, state, err
	}
	if failedTools != 0 {
		return ctx, state, status.Error(codes.Unavailable, "tool execution failed")
	}
	b.replay = nil
	// Only a trailing incomplete round may be replayed for dispatch. Tool adapters
	// reuse each persisted result; neither model facts nor completed reads repeat.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		assistant := messages[i]
		for _, call := range assistant.ToolCalls {
			paired := false
			for _, result := range messages[i+1:] {
				if result.Role == "tool" && result.ToolCallId == call.Id {
					paired = true
					break
				}
			}
			if !paired {
				b.replay = assistant
				break
			}
		}
		break
	}
	if b.replay == nil {
		// Waiting is decided only after all tools in the round have paired results.
		// An input accepted during those calls wins over the request to pause.
		r, err = b.service.repo.Change(ctx, r.ID, b.generation, func(tx *gorm.DB, current *model.Run) error {
			if current.WaitingReason != "" && current.InputVersion == current.AppliedInputVersion {
				current.Status, current.Stage = "waiting_input", "waiting_input"
				return nil
			}
			if current.InputVersion > current.AppliedInputVersion {
				return b.service.appendInputs(tx, current)
			}
			return nil
		})
		if err != nil {
			return ctx, state, err
		}
		if r.Status == "waiting_input" {
			return ctx, state, compose.Interrupt(ctx, "waiting for accepted Run input")
		}
		messages, err = b.service.messagesFor(ctx, r.ID)
		if err != nil {
			return ctx, state, err
		}
		last := messages[len(messages)-1]
		if last.Role == "assistant" && len(last.ToolCalls) == 0 {
			b.replay = last
		}
	}
	if b.replay == nil {
		messages, err = b.service.workingModelView(ctx, r, b.generation, b.config)
		if err != nil {
			if r.Status == "waiting_input" {
				b.run = r
				return ctx, state, compose.Interrupt(ctx, "source changes require explicit work input")
			}
			return ctx, state, err
		}
	}
	b.run = r
	b.messages = messages
	state.Messages = make([]*schema.Message, 0, len(messages))
	for _, m := range messages {
		state.Messages = append(state.Messages, agentMessage(m))
	}
	return ctx, state, nil
}

func (b *runAgentBridge) Generate(ctx context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	b.callMu.Lock()
	defer b.callMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.replay != nil {
		return agentMessage(b.replay), nil
	}
	if err := b.service.modelRound(ctx, b.run, b.generation, b.config, b.messages); err != nil {
		return nil, err
	}
	messages, err := b.service.messagesFor(ctx, b.run.ID)
	if err != nil {
		return nil, err
	}
	return agentMessage(messages[len(messages)-1]), nil
}

func (b *runAgentBridge) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := b.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

type runAgentTool struct {
	bridge *runAgentBridge
	frozen component.RunTool
}

func (t *runAgentTool) Info(context.Context) (*schema.ToolInfo, error) {
	var parameters jsonschema.Schema
	if err := json.Unmarshal(t.frozen.Parameters, &parameters); err != nil {
		return nil, err
	}
	return &schema.ToolInfo{Name: t.frozen.Name, Desc: t.frozen.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&parameters)}, nil
}
func (t *runAgentTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	b := t.bridge
	b.callMu.Lock()
	defer b.callMu.Unlock()
	r, err := b.service.executionBoundary(ctx, b.run, b.generation)
	if err != nil {
		return "", err
	}
	id := compose.GetToolCallID(ctx)
	messages, err := b.service.messagesFor(ctx, r.ID)
	if err != nil {
		return "", err
	}
	var call *llm.ToolCall
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallId == id {
			return m.Content, nil
		}
		for _, candidate := range m.ToolCalls {
			if candidate.Id == id && candidate.Function.Name == t.frozen.Name && candidate.Function.Arguments == arguments {
				call = candidate
			}
		}
	}
	if call == nil {
		return "", status.Error(codes.FailedPrecondition, "tool call is not in the persisted model response")
	}
	if err := b.service.performTool(ctx, r, b.generation, b.config, call); err != nil {
		return "", err
	}
	messages, err = b.service.messagesFor(ctx, r.ID)
	if err != nil {
		return "", err
	}
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallId == id {
			return m.Content, nil
		}
	}
	return "", status.Error(codes.FailedPrecondition, "missing persisted tool result")
}

// ADK checkpoints are executor-private control-flow state, not Run facts
// or authorization. Writes are fenced by the active execution generation.
type runAgentCheckpoint struct{ bridge *runAgentBridge }

func (c *runAgentCheckpoint) Get(ctx context.Context, id string) ([]byte, bool, error) {
	if id != c.bridge.run.ID {
		return nil, false, status.Error(codes.InvalidArgument, "checkpoint outside Run")
	}
	var checkpoint model.RunAgentCheckpoint
	err := c.bridge.service.svc.DB.WithContext(ctx).Where("run_id = ?", id).First(&checkpoint).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return checkpoint.Checkpoint, true, nil
}
func (c *runAgentCheckpoint) Set(ctx context.Context, id string, data []byte) error {
	if id != c.bridge.run.ID {
		return status.Error(codes.InvalidArgument, "checkpoint outside Run")
	}
	if len(data) > 16*1024*1024 {
		return status.Error(codes.ResourceExhausted, "agent checkpoint exceeds work budget")
	}
	_, err := c.bridge.service.repo.ChangeLifecycle(ctx, id, c.bridge.generation, func(tx *gorm.DB, current *model.Run) error {
		if err := c.bridge.service.recheckSources(ctx, tx, current); err != nil {
			return err
		}
		if current.Status == "stopping" || current.AccessRevoked || (current.Status == "waiting_authorization" && current.Stage != "pending_action") {
			return status.Error(codes.FailedPrecondition, "agent checkpoint authority superseded")
		}
		return tx.Exec(`INSERT INTO bot.run_agent_checkpoints (run_id, checkpoint, updated_at)
			VALUES (?, ?, NOW())
			ON CONFLICT (run_id) DO UPDATE SET checkpoint = EXCLUDED.checkpoint, updated_at = NOW()`, id, data).Error
	})
	return err
}

func (s *Service) runAgent(ctx context.Context, r *model.Run, generation int64, config ExecutionConfig) error {
	b := &runAgentBridge{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, service: s, run: r, generation: generation, config: config}
	tools := make([]tool.BaseTool, 0, len(config.Tools))
	for _, frozen := range config.Tools {
		tools = append(tools, &runAgentTool{bridge: b, frozen: frozen})
	}
	// Interrupt checkpoints consume ADK iterations too; only modelRound's
	// durable call count is the business MaxSteps budget.
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "aim_builtin", Model: b, MaxIterations: 2*config.MaxSteps + 2,
		Handlers: []adk.ChatModelAgentMiddleware{b},
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true, UnknownToolsHandler: func(context.Context, string, string) (string, error) {
			return "", status.Error(codes.PermissionDenied, "unknown-risk tool is not authorized")
		}}},
	})
	if err != nil {
		return err
	}
	store := &runAgentCheckpoint{bridge: b}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: store})
	// This loop handles only a final-result/accepted-input transaction race. ADK
	// exclusively owns model/tool iteration; the persisted call count owns budget.
	for {
		cancelOption, cancelAgent := adk.WithCancel()
		stop := context.AfterFunc(ctx, func() { cancelAgent(adk.WithAgentCancelMode(adk.CancelAfterChatModel | adk.CancelAfterToolCalls)) })
		_, saved, err := store.Get(ctx, r.ID)
		if err != nil {
			stop()
			return err
		}
		var iter *adk.AsyncIterator[*adk.AgentEvent]
		if saved {
			iter, err = runner.Resume(ctx, r.ID, cancelOption)
		} else {
			iter = runner.Run(ctx, nil, cancelOption, adk.WithCheckPointID(r.ID))
		}
		if err != nil {
			stop()
			return err
		}
		var executionErr error
		interrupted := false
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event.Err != nil && executionErr == nil {
				executionErr = event.Err
			}
			if event.Action != nil && event.Action.Interrupted != nil {
				interrupted = true
			}
			// Model/tool events are private. Never project reasoning or raw tool
			// results into public updates; RunRepo publishes allowlisted stages.
		}
		stop()
		// A closed event iterator is not proof that an RPC stopped. Wait for
		// the actual bridge call to exit before execute can confirm stopping.
		b.callMu.Lock()
		b.callMu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if executionErr != nil {
			if errors.Is(executionErr, adk.ErrExceedMaxIterations) {
				return status.Error(codes.ResourceExhausted, "model round limit exceeded")
			}
			return executionErr
		}
		if interrupted {
			return nil
		}
		r, err = s.executionBoundary(ctx, r, generation)
		if err != nil {
			return err
		}
		if r.InputVersion > r.AppliedInputVersion {
			continue
		}
		messages, err := s.messagesFor(ctx, r.ID)
		if err != nil {
			return err
		}
		last := messages[len(messages)-1]
		if last.Role != "assistant" || len(last.ToolCalls) != 0 || last.Content == "" {
			return status.Error(codes.FailedPrecondition, "model returned no final result")
		}
		if len(last.Content) > 262144 {
			return status.Error(codes.ResourceExhausted, "result too large")
		}
		r, err = s.repo.Change(ctx, r.ID, generation, func(_ *gorm.DB, current *model.Run) error {
			if current.InputVersion != current.AppliedInputVersion {
				return status.Error(codes.Aborted, "new input precedes completion")
			}
			current.Stage, current.ResultText, current.ResultType = "generated", last.Content, "text"
			current.ExecutorStatus, current.OutputStatus = "completed", "generated"
			current.ResultInvalidated = false
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
}
