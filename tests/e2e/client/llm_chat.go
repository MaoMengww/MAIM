package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	llmpb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const llmChatToolSchema = `{"type":"object","properties":{"text":{"type":"string","minLength":1}},"required":["text"],"additionalProperties":false,"anyOf":[{"properties":{"text":{"enum":["a","b"]}}}]}`

type llmChatObservation struct {
	Model         string `json:"model"`
	Token         string `json:"token"`
	Path          string `json:"path"`
	AuthDigest    string `json:"auth_digest"`
	State         string `json:"state"`
	UsageReported bool   `json:"usage_reported"`
	FinishSent    bool   `json:"finish_sent"`
	DoneSent      bool   `json:"done_sent"`
}

// llmChatScenario exercises public model CRUD and the real llm-gateway RPC.
// The HTTP fixture is only a controlled third-party provider; it never replaces
// an AIM service or writes AIM state directly.
func (d *driver) llmChatScenario() (result error) {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("llm_chat", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("llm_chat_outside", suffix)
	if err != nil {
		return err
	}
	modelName := "e2e-llm-normal-" + suffix
	modelID, err := d.fixtureModel(owner, "chat", modelName)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, d.request(http.MethodDelete, "/models/"+modelID.String(), owner.token, nil, nil))
	}()

	conn, err := grpc.NewClient(d.llmRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func(close func() error) {
		// Read-only transport cleanup does not affect the scenario verdict.
		_ = close()
	}(conn.Close)
	cli := llmpb.NewLLMGatewayClient(conn)
	ownerID := owner.id.String()
	contextFor := func(userID string, timeout time.Duration) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", userID)), cancel
	}
	resolve := func() (*llmpb.ModelExecutionConfig, error) {
		ctx, cancel := contextFor(ownerID, d.timeout)
		defer cancel()
		return cli.ResolveExecutionModel(ctx, &llmpb.ResolveExecutionModelReq{ModelId: modelID.String(), OwnerId: &ownerID})
	}
	request := func(cfg *llmpb.ModelExecutionConfig, token string) *llmpb.ChatReq {
		return &llmpb.ChatReq{
			ModelId: modelID.String(), OwnerId: &ownerID, ExecutionModel: cfg, Temperature: 0,
			Messages: []*llmpb.Message{{Role: "system", Content: "e2e system"}, {Role: "user", Content: token}},
		}
	}
	chat := func(req *llmpb.ChatReq) (*llmpb.ChatResp, error) {
		ctx, cancel := contextFor(ownerID, d.timeout)
		defer cancel()
		return cli.Chat(ctx, req)
	}
	stream := func(req *llmpb.ChatReq) ([]*llmpb.ChatStreamChunk, error) {
		ctx, cancel := contextFor(ownerID, d.timeout)
		defer cancel()
		reader, err := cli.ChatStream(ctx, req)
		if err != nil {
			return nil, err
		}
		var chunks []*llmpb.ChatStreamChunk
		for {
			chunk, recvErr := reader.Recv()
			if errors.Is(recvErr, io.EOF) {
				return chunks, nil
			}
			if recvErr != nil {
				return chunks, recvErr
			}
			chunks = append(chunks, chunk)
		}
	}
	setModel := func(fields map[string]any) error {
		return d.request(http.MethodPut, "/models/"+modelID.String(), owner.token, fields, nil)
	}
	setName := func(name string) error {
		if err := setModel(map[string]any{"model_name": name}); err != nil {
			return err
		}
		modelName = name
		return nil
	}
	apiKey := "e2e-fixture-not-a-secret"
	setProvider := func(provider, baseURL, key string) error {
		if err := setModel(map[string]any{"provider": provider, "base_url": baseURL, "api_key": key}); err != nil {
			return err
		}
		apiKey = key
		return nil
	}
	tools := []*llmpb.ToolDef{
		{Name: "fixture_echo_a", Description: "first", Parameters: []byte(llmChatToolSchema)},
		{Name: "fixture_echo_b", Description: "second", Parameters: []byte(llmChatToolSchema)},
	}

	// A different authenticated user cannot invoke the private registry entry.
	outsiderID := outsider.id.String()
	ctx, cancel := contextFor(outsiderID, d.timeout)
	_, err = cli.Chat(ctx, &llmpb.ChatReq{ModelId: modelID.String(), OwnerId: &outsiderID, Messages: []*llmpb.Message{{Role: "user", Content: "LLM_FORBIDDEN"}}})
	cancel()
	if status.Code(err) != codes.PermissionDenied {
		return fmt.Errorf("llm-chat registry visibility: expected PermissionDenied, got %v", err)
	}

	cfg, err := resolve()
	if err != nil {
		return fmt.Errorf("llm-chat resolve: %w", err)
	}
	started := time.Now()
	resp, err := chat(request(cfg, "LLM_NORMAL_SYNC"))
	elapsed := time.Since(started)
	if err != nil {
		return fmt.Errorf("llm-chat sync: %w", err)
	}
	if err := requireChatResponse(resp, modelName, "fixture-chat:LLM_NORMAL_SYNC", "stop"); err != nil {
		return fmt.Errorf("llm-chat sync: %w", err)
	}
	if err := requireFirstToken(resp.FirstTokenLatencyMs, elapsed); err != nil {
		return fmt.Errorf("llm-chat sync TTFT: %w", err)
	}
	if err := requireReportedUsage(resp.Usage, 7, 5, 12); err != nil {
		return fmt.Errorf("llm-chat sync usage: %w", err)
	}
	if _, err := d.requireLLMChatObservation(modelName, "LLM_NORMAL_SYNC", "completed", "/v1/chat/completions", apiKey); err != nil {
		return err
	}

	started = time.Now()
	chunks, err := stream(request(cfg, "LLM_NORMAL_STREAM"))
	elapsed = time.Since(started)
	if err != nil {
		return fmt.Errorf("llm-chat stream: %w", err)
	}
	if err := requireChatStream(chunks, "fixture-chat:LLM_NORMAL_STREAM", "stop", elapsed); err != nil {
		return fmt.Errorf("llm-chat stream: %w", err)
	}
	if err := requireReportedUsage(lastChatUsage(chunks), 7, 5, 12); err != nil {
		return fmt.Errorf("llm-chat stream usage: %w", err)
	}
	if _, err := d.requireLLMChatObservation(modelName, "LLM_NORMAL_STREAM", "completed", "/v1/chat/completions", apiKey); err != nil {
		return err
	}

	// The provider emits tool indices 1 then 0 and fragments both arguments.
	// The RPC must return deterministic index order, then preserve that exact
	// assistant trajectory when paired tool results are replayed.
	openAITools := request(cfg, "LLM_TOOLS_OPENAI")
	openAITools.Tools = tools
	started = time.Now()
	toolResp, err := chat(openAITools)
	elapsed = time.Since(started)
	if err != nil {
		return fmt.Errorf("llm-chat OpenAI tools: %w", err)
	}
	if err := requireToolResponse(toolResp, ""); err != nil {
		return fmt.Errorf("llm-chat OpenAI tools: %w", err)
	}
	if err := requireFirstToken(toolResp.FirstTokenLatencyMs, elapsed); err != nil {
		return fmt.Errorf("llm-chat tool TTFT: %w", err)
	}
	if _, err := d.requireLLMChatObservation(modelName, "LLM_TOOLS_OPENAI", "completed", "/v1/chat/completions", apiKey); err != nil {
		return err
	}
	if err := d.replayLLMChatTools(chat, request, cfg, tools, toolResp.Choices[0].Message, "LLM_REPLAY_OPENAI", modelName, apiKey); err != nil {
		return err
	}
	toolChunks, err := stream(openAITools)
	if err != nil {
		return fmt.Errorf("llm-chat OpenAI tool stream: %w", err)
	}
	assistant, err := requireToolStream(toolChunks, "")
	if err != nil {
		return err
	}
	if err := d.replayLLMChatTools(chat, request, cfg, tools, assistant, "LLM_REPLAY_STREAM_OPENAI", modelName, apiKey); err != nil {
		return err
	}

	// A configured DeepSeek provider must preserve private reasoning through a
	// tool-call response and the next request's assistant replay.
	if err := setProvider("deepseek", d.provider+"/v1", "e2e-deepseek-key"); err != nil {
		return err
	}
	if err := setName("e2e-llm-deepseek-" + suffix); err != nil {
		return err
	}
	cfg, err = resolve()
	if err != nil {
		return err
	}
	deepseekTools := request(cfg, "LLM_TOOLS_DEEPSEEK")
	deepseekTools.Tools = tools
	started = time.Now()
	toolResp, err = chat(deepseekTools)
	elapsed = time.Since(started)
	if err != nil {
		return fmt.Errorf("llm-chat DeepSeek tools: %w", err)
	}
	if err := requireToolResponse(toolResp, "private-tool-reasoning"); err != nil {
		return fmt.Errorf("llm-chat DeepSeek tools: %w", err)
	}
	if err := requireFirstToken(toolResp.FirstTokenLatencyMs, elapsed); err != nil {
		return fmt.Errorf("llm-chat reasoning TTFT: %w", err)
	}
	if err := d.replayLLMChatTools(chat, request, cfg, tools, toolResp.Choices[0].Message, "LLM_REPLAY_DEEPSEEK", modelName, apiKey); err != nil {
		return err
	}
	toolChunks, err = stream(deepseekTools)
	if err != nil {
		return fmt.Errorf("llm-chat DeepSeek tool stream: %w", err)
	}
	assistant, err = requireToolStream(toolChunks, "private-tool-reasoning")
	if err != nil {
		return err
	}
	if err := d.replayLLMChatTools(chat, request, cfg, tools, assistant, "LLM_REPLAY_STREAM_DEEPSEEK", modelName, apiKey); err != nil {
		return err
	}
	chunks, err = stream(request(cfg, "LLM_DEEPSEEK_CACHE"))
	if err != nil {
		return fmt.Errorf("llm-chat DeepSeek cache: %w", err)
	}
	usage := lastChatUsage(chunks)
	if usage == nil || usage.CachedInputTokens == nil || *usage.CachedInputTokens != 2 || usage.CacheWriteTokens != nil {
		return errors.New("llm-chat DeepSeek cache hit/missing write optionality was not preserved")
	}

	// Qwen also uses its configured OpenAI-compatible endpoint rather than a
	// hard-coded public host.
	if err := setProvider("qwen", d.provider+"/v1", "e2e-qwen-key"); err != nil {
		return err
	}
	if err := setName("e2e-llm-qwen-" + suffix); err != nil {
		return err
	}
	cfg, err = resolve()
	if err != nil {
		return err
	}
	resp, err = chat(request(cfg, "LLM_QWEN_CONFIGURED"))
	if err != nil {
		return fmt.Errorf("llm-chat configured Qwen: %w", err)
	}
	if err := requireChatResponse(resp, modelName, "fixture-chat:LLM_QWEN_CONFIGURED", "stop"); err != nil {
		return fmt.Errorf("llm-chat configured Qwen: %w", err)
	}
	if _, err := d.requireLLMChatObservation(modelName, "LLM_QWEN_CONFIGURED", "completed", "/v1/chat/completions", apiKey); err != nil {
		return err
	}

	if err := setProvider("openai", d.provider+"/v1", "e2e-usage-key"); err != nil {
		return err
	}
	if err := setName("e2e-llm-usage-" + suffix); err != nil {
		return err
	}
	cfg, err = resolve()
	if err != nil {
		return err
	}
	for _, token := range []string{"LLM_USAGE_ONLY", "LLM_NO_USAGE", "LLM_CACHE", "LLM_CACHE_ZERO", "LLM_ZERO"} {
		chunks, callErr := stream(request(cfg, token))
		if callErr != nil {
			return fmt.Errorf("llm-chat usage mode %s: %w", token, callErr)
		}
		if err := requireChatStream(chunks, "fixture-chat:"+token, "stop", 0); err != nil {
			return fmt.Errorf("llm-chat usage mode %s: %w", token, err)
		}
		usage = lastChatUsage(chunks)
		switch token {
		case "LLM_NO_USAGE":
			if usage != nil {
				return errors.New("llm-chat missing provider usage became SDK zero usage")
			}
		case "LLM_ZERO":
			if usage == nil || usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0 || usage.CachedInputTokens != nil || usage.CacheWriteTokens != nil {
				return errors.New("llm-chat explicit zero usage or unsupported cache optionality was lost")
			}
		case "LLM_CACHE":
			if usage == nil || usage.CachedInputTokens == nil || *usage.CachedInputTokens != 2 || usage.CacheWriteTokens == nil || *usage.CacheWriteTokens != 3 {
				return errors.New("llm-chat cache read/write usage was not preserved")
			}
		case "LLM_CACHE_ZERO":
			if usage == nil || usage.CachedInputTokens == nil || *usage.CachedInputTokens != 0 || usage.CacheWriteTokens == nil || *usage.CacheWriteTokens != 0 {
				return errors.New("llm-chat explicit zero cache usage became unsupported/missing")
			}
		default:
			if err := requireReportedUsage(usage, 7, 5, 12); err != nil || !hasUsageOnlyChunk(chunks) {
				return fmt.Errorf("llm-chat usage-only chunk was not preserved: %v", err)
			}
		}
		observed, err := d.requireLLMChatObservation(modelName, token, "completed", "/v1/chat/completions", apiKey)
		if err != nil {
			return err
		}
		if observed.UsageReported != (token != "LLM_NO_USAGE") || !observed.FinishSent || !observed.DoneSent {
			return fmt.Errorf("llm-chat provider completion evidence mismatch for %s", token)
		}
	}

	if err := setName("e2e-llm-failure-" + suffix); err != nil {
		return err
	}
	cfg, err = resolve()
	if err != nil {
		return err
	}
	for _, failure := range []struct {
		token, state string
		finish, done bool
	}{
		{"LLM_NO_DONE", "truncated", true, false},
		{"LLM_NO_FINISH", "truncated", false, true},
		{"LLM_HTTP_ERROR", "http_error", false, false},
		{"LLM_STREAM_ERROR", "stream_error", false, false},
	} {
		_, callErr := stream(request(cfg, failure.token))
		if callErr == nil || status.Code(callErr) != codes.Unavailable {
			return fmt.Errorf("llm-chat failure %s: expected Unavailable, got %v", failure.token, callErr)
		}
		if err := requireProviderErrorRedaction(callErr, failure.token, apiKey); err != nil {
			return err
		}
		observed, err := d.requireLLMChatObservation(modelName, failure.token, failure.state, "/v1/chat/completions", apiKey)
		if err != nil {
			return err
		}
		if observed.FinishSent != failure.finish || observed.DoneSent != failure.done {
			return fmt.Errorf("llm-chat %s finish/DONE fixture evidence mismatch", failure.token)
		}
	}
	// Many individually legal SSE frames must not permit unbounded aggregate
	// content or tool arguments. Both public modes fail and release admission.
	for _, token := range []string{"LLM_OVERSIZED_CONTENT", "LLM_OVERSIZED_TOOLS"} {
		for _, invoke := range []func(*llmpb.ChatReq) error{
			func(req *llmpb.ChatReq) error { _, err := chat(req); return err },
			func(req *llmpb.ChatReq) error { _, err := stream(req); return err },
		} {
			callErr := invoke(request(cfg, token))
			if status.Code(callErr) != codes.Unavailable {
				return fmt.Errorf("llm-chat unbounded aggregate %s: expected Unavailable, got %v", token, callErr)
			}
			if err := requireProviderErrorRedaction(callErr, token, apiKey); err != nil {
				return err
			}
			if _, err := chat(request(cfg, "LLM_AFTER_BUDGET")); err != nil {
				return fmt.Errorf("llm-chat response-budget concurrency release: %w", err)
			}
		}
	}

	// Cancel only after receiving actual content, then require the provider to
	// observe the underlying HTTP stream closing before any finish or DONE.
	cancelCtx, cancelStream := contextFor(ownerID, d.timeout)
	reader, err := cli.ChatStream(cancelCtx, request(cfg, "LLM_CANCEL"))
	if err != nil {
		cancelStream()
		return err
	}
	gotContent := false
	for !gotContent {
		chunk, recvErr := reader.Recv()
		if recvErr != nil {
			cancelStream()
			return fmt.Errorf("llm-chat cancel before content: %w", recvErr)
		}
		for _, choice := range chunk.GetChoices() {
			gotContent = gotContent || choice.GetDelta().GetContent() != ""
		}
	}
	cancelStream()
	for {
		_, recvErr := reader.Recv()
		if recvErr == nil {
			continue
		}
		if status.Code(recvErr) != codes.Canceled {
			return fmt.Errorf("llm-chat cancellation: expected Canceled, got %v", recvErr)
		}
		break
	}
	observed, err := d.requireLLMChatObservation(modelName, "LLM_CANCEL", "cancelled", "/v1/chat/completions", apiKey)
	if err != nil {
		return err
	}
	if observed.FinishSent || observed.DoneSent {
		return errors.New("llm-chat cancelled provider stream emitted completion evidence")
	}
	if _, err := chat(request(cfg, "LLM_AFTER_CANCEL")); err != nil {
		return fmt.Errorf("llm-chat post-cancel concurrency release: %w", err)
	}

	// Endpoint and credential changes invalidate the authenticated snapshot
	// before any request is sent; a new snapshot uses the current credential.
	if err := setName("e2e-llm-snapshot-" + suffix); err != nil {
		return err
	}
	if err := setProvider("openai", d.provider+"/v1", "old-e2e-key"); err != nil {
		return err
	}
	oldCfg, err := resolve()
	if err != nil {
		return err
	}
	if err := setProvider("openai", d.provider+"/v1-changed", "old-e2e-key"); err != nil {
		return err
	}
	_, snapshotErr := chat(request(oldCfg, "LLM_SNAPSHOT_OLD_ROUTE"))
	if status.Code(snapshotErr) != codes.AlreadyExists {
		return fmt.Errorf("llm-chat stale endpoint snapshot: expected AlreadyExists, got %v", snapshotErr)
	}
	if found, err := d.hasLLMChatObservation(modelName, "LLM_SNAPSHOT_OLD_ROUTE"); err != nil || found {
		return fmt.Errorf("llm-chat stale endpoint reached provider: found=%t err=%v", found, err)
	}
	currentCfg, err := resolve()
	if err != nil {
		return err
	}
	if _, err := chat(request(currentCfg, "LLM_SNAPSHOT_NEW_ROUTE")); err != nil {
		return fmt.Errorf("llm-chat current endpoint snapshot: %w", err)
	}
	if _, err := d.requireLLMChatObservation(modelName, "LLM_SNAPSHOT_NEW_ROUTE", "completed", "/v1-changed/chat/completions", apiKey); err != nil {
		return err
	}
	if err := setModel(map[string]any{"api_key": "rotated-e2e-key"}); err != nil {
		return err
	}
	apiKey = "rotated-e2e-key"
	if _, callErr := chat(request(currentCfg, "LLM_KEY_STALE")); status.Code(callErr) != codes.InvalidArgument {
		return fmt.Errorf("llm-chat stale credential snapshot: expected InvalidArgument, got %v", callErr)
	}
	if found, err := d.hasLLMChatObservation(modelName, "LLM_KEY_STALE"); err != nil || found {
		return fmt.Errorf("llm-chat stale credential reached provider: found=%t err=%v", found, err)
	}
	currentCfg, err = resolve()
	if err != nil {
		return err
	}
	if _, err := chat(request(currentCfg, "LLM_KEY_CHANGED")); err != nil {
		return fmt.Errorf("llm-chat current credential snapshot: %w", err)
	}
	if _, err := d.requireLLMChatObservation(modelName, "LLM_KEY_CHANGED", "completed", "/v1-changed/chat/completions", apiKey); err != nil {
		return err
	}
	return nil
}

func (d *driver) replayLLMChatTools(chat func(*llmpb.ChatReq) (*llmpb.ChatResp, error), request func(*llmpb.ModelExecutionConfig, string) *llmpb.ChatReq, cfg *llmpb.ModelExecutionConfig, tools []*llmpb.ToolDef, assistant *llmpb.Message, token, model, apiKey string) error {
	replay := request(cfg, token)
	replay.Tools = tools
	replay.Messages = []*llmpb.Message{
		{Role: "system", Content: "e2e system"},
		{Role: "user", Content: token},
		assistant,
		{Role: "tool", ToolCallId: "call-a", Content: "fixture-tool:a"},
		{Role: "tool", ToolCallId: "call-b", Content: "fixture-tool:b"},
	}
	resp, err := chat(replay)
	if err != nil {
		return fmt.Errorf("llm-chat tool replay %s: %w", token, err)
	}
	if err := requireChatResponse(resp, model, "fixture-chat:"+token, "stop"); err != nil {
		return fmt.Errorf("llm-chat tool replay %s: %w", token, err)
	}
	_, err = d.requireLLMChatObservation(model, token, "completed", "/v1/chat/completions", apiKey)
	return err
}

func requireChatResponse(resp *llmpb.ChatResp, model, content, finish string) error {
	if resp == nil || resp.Model != model || len(resp.Choices) != 1 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != content || resp.Choices[0].FinishReason != finish {
		return errors.New("response model/content/finish contract mismatch")
	}
	return nil
}

func requireToolResponse(resp *llmpb.ChatResp, reasoning string) error {
	if resp == nil || len(resp.Choices) != 1 || resp.Choices[0].FinishReason != "tool_calls" || resp.Choices[0].Message == nil {
		return errors.New("tool response or finish reason missing")
	}
	message := resp.Choices[0].Message
	if message.ReasoningContent != reasoning || len(message.ToolCalls) != 2 {
		return errors.New("tool reasoning or call count mismatch")
	}
	want := []struct{ id, name, arguments string }{{"call-a", "fixture_echo_a", `{"text":"a"}`}, {"call-b", "fixture_echo_b", `{"text":"b"}`}}
	for i, expected := range want {
		call := message.ToolCalls[i]
		if call == nil || call.Id != expected.id || call.Type != "function" || call.Function == nil || call.Function.Name != expected.name || call.Function.Arguments != expected.arguments {
			return fmt.Errorf("tool call %d order/identity/arguments mismatch", i)
		}
	}
	return nil
}

func requireToolStream(chunks []*llmpb.ChatStreamChunk, reasoning string) (*llmpb.Message, error) {
	message := &llmpb.Message{Role: "assistant"}
	calls := map[int32]*llmpb.ToolCall{}
	finish := ""
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			delta := choice.GetDelta()
			message.Content += delta.GetContent()
			message.ReasoningContent += delta.GetReasoningContent()
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
			for _, delta := range delta.GetToolCalls() {
				if delta.Index == nil || *delta.Index < 0 || *delta.Index > 1 {
					return nil, errors.New("tool stream missing/invalid index")
				}
				call := calls[*delta.Index]
				if call == nil {
					call = &llmpb.ToolCall{Function: &llmpb.ToolCall_Function{}}
					calls[*delta.Index] = call
				}
				if delta.Id != "" {
					call.Id = delta.Id
				}
				if delta.Type != "" {
					call.Type = delta.Type
				}
				call.Function.Name += delta.GetFunction().GetName()
				call.Function.Arguments += delta.GetFunction().GetArguments()
			}
		}
	}
	message.ToolCalls = []*llmpb.ToolCall{calls[0], calls[1]}
	resp := &llmpb.ChatResp{Choices: []*llmpb.ChatResp_Choice{{Message: message, FinishReason: finish}}}
	if message.Content != "" {
		return nil, errors.New("private tool reasoning leaked into stream content")
	}
	return message, requireToolResponse(resp, reasoning)
}

func requireFirstToken(first *int64, elapsed time.Duration) error {
	if first == nil || *first < 120 {
		return fmt.Errorf("role-only delta counted as first token: %v", first)
	}
	if elapsed > 0 && time.Duration(*first)*time.Millisecond+150*time.Millisecond >= elapsed {
		return fmt.Errorf("first token %dms was completion latency %s", *first, elapsed)
	}
	return nil
}

func requireChatStream(chunks []*llmpb.ChatStreamChunk, content, finish string, elapsed time.Duration) error {
	var got strings.Builder
	var first *int64
	finishCount := 0
	seenActual := false
	for _, chunk := range chunks {
		for _, choice := range chunk.GetChoices() {
			delta := choice.GetDelta()
			actual := delta.GetContent() != "" || delta.GetReasoningContent() != "" || len(delta.GetToolCalls()) > 0
			if !seenActual && !actual && chunk.FirstTokenLatencyMs != nil {
				return errors.New("role-only stream delta carried first-token latency")
			}
			if actual && !seenActual {
				seenActual = true
				first = chunk.FirstTokenLatencyMs
			}
			got.WriteString(delta.GetContent())
			if choice.GetFinishReason() != "" {
				finishCount++
				if choice.GetFinishReason() != finish {
					return fmt.Errorf("unexpected finish reason %q", choice.GetFinishReason())
				}
			}
		}
	}
	if got.String() != content || finishCount != 1 {
		return fmt.Errorf("stream content/finish mismatch content=%q finishes=%d", got.String(), finishCount)
	}
	if elapsed > 0 {
		return requireFirstToken(first, elapsed)
	}
	return nil
}

func requireReportedUsage(usage *llmpb.UsageInfo, prompt, completion, total int32) error {
	if usage == nil || usage.PromptTokens != prompt || usage.CompletionTokens != completion || usage.TotalTokens != total || usage.CachedInputTokens != nil || usage.CacheWriteTokens != nil {
		return errors.New("reported usage values or unsupported cache optionality mismatch")
	}
	return nil
}

func lastChatUsage(chunks []*llmpb.ChatStreamChunk) *llmpb.UsageInfo {
	for i := len(chunks) - 1; i >= 0; i-- {
		if chunks[i].Usage != nil {
			return chunks[i].Usage
		}
	}
	return nil
}

func hasUsageOnlyChunk(chunks []*llmpb.ChatStreamChunk) bool {
	for _, chunk := range chunks {
		if chunk.Usage != nil && len(chunk.Choices) == 0 {
			return true
		}
	}
	return false
}

func requireProviderErrorRedaction(err error, token, apiKey string) error {
	if err == nil {
		return errors.New("provider error was accepted")
	}
	message := err.Error()
	for _, secret := range []string{"LLM_PROVIDER_PRIVATE_BODY", token, apiKey, "Bearer " + apiKey} {
		if secret != "" && strings.Contains(message, secret) {
			return fmt.Errorf("provider error leaked controlled private value %q", secret)
		}
	}
	return nil
}

func bearerDigest(apiKey string) string {
	digest := sha256.Sum256([]byte("Bearer " + apiKey))
	return hex.EncodeToString(digest[:])
}

func (d *driver) llmChatObservations(model string) ([]llmChatObservation, error) {
	var response struct {
		Requests []llmChatObservation `json:"requests"`
	}
	if err := d.executorProvider("/llm-chat/observe", map[string]string{"model": model}, &response); err != nil {
		return nil, err
	}
	return response.Requests, nil
}

func (d *driver) hasLLMChatObservation(model, token string) (bool, error) {
	rows, err := d.llmChatObservations(model)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Token == token {
			return true, nil
		}
	}
	return false, nil
}

func (d *driver) requireLLMChatObservation(model, token, state, path, apiKey string) (llmChatObservation, error) {
	deadline := time.Now().Add(d.timeout)
	var last llmChatObservation
	for time.Now().Before(deadline) {
		rows, err := d.llmChatObservations(model)
		if err != nil {
			return last, err
		}
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].Token != token {
				continue
			}
			last = rows[i]
			if last.State == state {
				if last.Path != path || last.AuthDigest != bearerDigest(apiKey) {
					return last, fmt.Errorf("llm-chat provider route/credential mismatch for %s", token)
				}
				return last, nil
			}
			if last.State == "cancel_not_observed" || last.State == "invalid_request" || last.State == "invalid_schema" || last.State == "invalid_replay" {
				return last, fmt.Errorf("llm-chat provider state=%s for %s", last.State, token)
			}
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	return last, fmt.Errorf("llm-chat provider did not observe %s state=%s; last=%s", token, state, last.State)
}
