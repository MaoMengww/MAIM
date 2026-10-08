package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

// AdapterVersion changes when the wire rendering or trajectory contract changes.
const AdapterVersion = "openai-chat-v2"

func ChatBaseURL(provider, baseURL string) (string, error) {
	if baseURL == "" {
		switch provider {
		case "openai":
			baseURL = "https://api.openai.com/v1"
		case "deepseek":
			baseURL = "https://api.deepseek.com"
		case "qwen":
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		default:
			return "", fmt.Errorf("provider requires base_url")
		}
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid provider base_url")
	}
	return strings.TrimRight(baseURL, "/"), nil
}

// BuildChatBody preserves the recorded message/tool order and canonicalizes schemas
// without converting them through another schema model that could drop keywords.
func BuildChatBody(req *pb.ChatReq, cfg *pb.ModelExecutionConfig, apiKey string) (map[string]any, error) {
	if req == nil || cfg == nil || cfg.ModelName == "" || len(req.Messages) == 0 {
		return nil, fmt.Errorf("model and messages required")
	}
	messages := make([]map[string]any, 0, len(req.Messages))
	pending := map[string]bool{}
	seen := map[string]bool{}
	for _, msg := range req.Messages {
		if msg == nil {
			return nil, fmt.Errorf("nil message")
		}
		if len(pending) > 0 && msg.Role != "tool" {
			return nil, fmt.Errorf("tool calls require adjacent paired results")
		}
		switch msg.Role {
		case "system", "developer", "user", "assistant":
			if msg.ToolCallId != "" || (msg.Role != "assistant" && len(msg.ToolCalls) > 0) {
				return nil, fmt.Errorf("invalid message tool role")
			}
		case "tool":
			if !pending[msg.ToolCallId] || len(msg.ToolCalls) > 0 {
				return nil, fmt.Errorf("unpaired tool result")
			}
			delete(pending, msg.ToolCallId)
		default:
			return nil, fmt.Errorf("unsupported message role")
		}
		m := map[string]any{"role": msg.Role, "content": msg.Content}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		if msg.ToolCallId != "" {
			m["tool_call_id"] = msg.ToolCallId
		}
		if msg.ReasoningContent != "" && cfg.Provider == "deepseek" {
			if msg.Role != "assistant" {
				return nil, fmt.Errorf("unsupported reasoning replay")
			}
			m["reasoning_content"] = msg.ReasoningContent
		}
		if len(msg.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				if call == nil || call.Id == "" || seen[call.Id] || call.Function == nil || call.Function.Name == "" || (call.Type != "" && call.Type != "function") {
					return nil, fmt.Errorf("invalid assistant tool call")
				}
				seen[call.Id], pending[call.Id] = true, true
				calls = append(calls, map[string]any{"id": call.Id, "type": "function", "function": map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments}})
			}
			m["tool_calls"] = calls
		}
		messages = append(messages, m)
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("missing tool results")
	}
	body := map[string]any{"model": cfg.ModelName, "messages": messages, "stream": true, "stream_options": map[string]any{"include_usage": true}}
	if req.ExecutionModel != nil || req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	names := map[string]bool{}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		if tool == nil || tool.Name == "" || names[tool.Name] {
			return nil, fmt.Errorf("invalid or duplicate tool name")
		}
		names[tool.Name] = true
		fn := map[string]any{"name": tool.Name, "description": tool.Description}
		if len(tool.Parameters) > 0 {
			dec := json.NewDecoder(bytes.NewReader(tool.Parameters))
			dec.UseNumber()
			var params map[string]any
			if err := dec.Decode(&params); err != nil || params == nil {
				return nil, fmt.Errorf("invalid tool parameters")
			}
			var extra any
			if err := dec.Decode(&extra); err != io.EOF {
				return nil, fmt.Errorf("invalid trailing tool parameters")
			}
			fn["parameters"] = params
		}
		tools = append(tools, map[string]any{"type": "function", "function": fn})
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	// Only the actual OpenAI endpoint is known to support prompt_cache_key.
	// Auth identity is hashed, never sent as a credential or logged.
	u, _ := url.Parse(cfg.BaseUrl)
	if cfg.Provider == "openai" && u != nil && u.Hostname() == "api.openai.com" && req.CacheScope != "" {
		scope, _ := json.Marshal([]string{AdapterVersion, apiKey, cfg.ModelId, cfg.ModelName, cfg.Provider, cfg.BaseUrl, cfg.Version, req.GetOwnerId(), req.GetBotId(), req.CacheScope})
		sum := sha256.Sum256(scope)
		body["prompt_cache_key"] = hex.EncodeToString(sum[:])
	}
	return body, nil
}

type providerUsage struct {
	PromptTokens         int32  `json:"prompt_tokens"`
	CompletionTokens     int32  `json:"completion_tokens"`
	TotalTokens          int32  `json:"total_tokens"`
	PromptCacheHitTokens *int32 `json:"prompt_cache_hit_tokens"`
	PromptDetails        *struct {
		CachedTokens     *int32 `json:"cached_tokens"`
		CacheWriteTokens *int32 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u *providerUsage) proto(provider string) *pb.UsageInfo {
	if u == nil {
		return nil
	}
	out := &pb.UsageInfo{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	if u.PromptDetails != nil {
		out.CachedInputTokens = u.PromptDetails.CachedTokens
		out.CacheWriteTokens = u.PromptDetails.CacheWriteTokens
	}
	if provider == "deepseek" && u.PromptCacheHitTokens != nil {
		out.CachedInputTokens = u.PromptCacheHitTokens
	}
	// DeepSeek miss tokens are uncached inputs, not reported cache writes.
	return out
}

type providerMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	ToolCalls        []struct {
		Index    *int32 `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

func (m providerMessage) proto() *pb.Message {
	out := &pb.Message{Role: m.Role, Content: m.Content, ReasoningContent: m.ReasoningContent}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, &pb.ToolCall{Id: tc.ID, Type: tc.Type, Index: tc.Index, Function: &pb.ToolCall_Function{Name: tc.Function.Name, Arguments: tc.Function.Arguments}})
	}
	return out
}

type RequestError struct{ Err error }

func (e *RequestError) Error() string { return e.Err.Error() }

// StreamChat reports provider deltas verbatim, including final usage-only chunks.
// A clean socket EOF without [DONE] is not a successful completion.
func StreamChat(ctx context.Context, req *pb.ChatReq, cfg *pb.ModelExecutionConfig, apiKey string, send func(*pb.ChatStreamChunk) error) error {
	body, err := BuildChatBody(req, cfg, apiKey)
	if err != nil {
		return &RequestError{Err: err}
	}
	start := time.Now()
	response, err := doRequestStream(ctx, cfg.BaseUrl+"/chat/completions", apiKey, body)
	if err != nil {
		return err
	}
	defer response.Close()
	scanner := bufio.NewScanner(response)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var firstToken *int64
	finished := map[int32]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			if len(finished) == 0 {
				return fmt.Errorf("provider returned no choices")
			}
			for _, done := range finished {
				if !done {
					return fmt.Errorf("provider completion has no finish reason")
				}
			}
			return nil
		}
		if data == "" {
			continue
		}
		var wire struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Index        int32           `json:"index"`
				Delta        providerMessage `json:"delta"`
				FinishReason string          `json:"finish_reason"`
			} `json:"choices"`
			Usage *providerUsage  `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &wire); err != nil {
			return fmt.Errorf("invalid provider stream payload")
		}
		if len(wire.Error) > 0 && string(wire.Error) != "null" {
			return fmt.Errorf("provider stream rejected")
		}
		out := &pb.ChatStreamChunk{Id: wire.ID, Model: wire.Model, Usage: wire.Usage.proto(cfg.Provider)}
		for _, c := range wire.Choices {
			if _, exists := finished[c.Index]; !exists {
				finished[c.Index] = false
			}
			if c.FinishReason != "" {
				finished[c.Index] = true
			}
			if firstToken == nil && (c.Delta.Content != "" || c.Delta.ReasoningContent != "" || len(c.Delta.ToolCalls) > 0) {
				elapsed := time.Since(start).Milliseconds()
				firstToken = &elapsed
			}
			out.Choices = append(out.Choices, &pb.ChatStreamChunk_Choice{Index: c.Index, Delta: c.Delta.proto(), FinishReason: c.FinishReason})
		}
		out.FirstTokenLatencyMs = firstToken
		if err := send(out); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("provider stream read failed: %w", err)
	}
	return fmt.Errorf("provider stream ended before completion")
}

// CompleteChat uses the same wire request as streaming so TTFT is measured from
// the first content/tool delta, rather than invented from completion latency.
func CompleteChat(ctx context.Context, req *pb.ChatReq, cfg *pb.ModelExecutionConfig, apiKey string) (*pb.ChatResp, error) {
	out := &pb.ChatResp{}
	choices := map[int32]*pb.ChatResp_Choice{}
	type textAccum struct{ content, reasoning strings.Builder }
	type toolAccum struct {
		call            *pb.ToolCall
		name, arguments strings.Builder
	}
	texts := map[int32]*textAccum{}
	calls := map[int32]map[int32]*toolAccum{}
	err := StreamChat(ctx, req, cfg, apiKey, func(chunk *pb.ChatStreamChunk) error {
		if chunk.Id != "" {
			out.Id = chunk.Id
		}
		if chunk.Model != "" {
			out.Model = chunk.Model
		}
		if chunk.Usage != nil {
			out.Usage = chunk.Usage
		}
		if chunk.FirstTokenLatencyMs != nil {
			out.FirstTokenLatencyMs = chunk.FirstTokenLatencyMs
		}
		for _, c := range chunk.Choices {
			choice := choices[c.Index]
			if choice == nil {
				choice = &pb.ChatResp_Choice{Index: c.Index, Message: &pb.Message{Role: "assistant"}}
				choices[c.Index], texts[c.Index] = choice, &textAccum{}
			}
			texts[c.Index].content.WriteString(c.Delta.Content)
			texts[c.Index].reasoning.WriteString(c.Delta.ReasoningContent)
			if c.FinishReason != "" {
				choice.FinishReason = c.FinishReason
			}
			for _, delta := range c.Delta.ToolCalls {
				if delta.Index == nil || *delta.Index < 0 {
					return fmt.Errorf("provider tool delta missing index")
				}
				if calls[c.Index] == nil {
					calls[c.Index] = map[int32]*toolAccum{}
				}
				acc := calls[c.Index][*delta.Index]
				if acc == nil {
					acc = &toolAccum{call: &pb.ToolCall{Function: &pb.ToolCall_Function{}}}
					calls[c.Index][*delta.Index] = acc
				}
				call := acc.call
				if delta.Id != "" {
					if call.Id != "" && call.Id != delta.Id {
						return fmt.Errorf("provider changed tool call identity")
					}
					call.Id = delta.Id
				}
				if delta.Type != "" {
					call.Type = delta.Type
				}
				acc.name.WriteString(delta.Function.Name)
				acc.arguments.WriteString(delta.Function.Arguments)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	indices := slices.Sorted(maps.Keys(choices))
	for _, index := range indices {
		choice := choices[index]
		choice.Message.Content = texts[index].content.String()
		choice.Message.ReasoningContent = texts[index].reasoning.String()
		if choice.FinishReason == "" {
			return nil, fmt.Errorf("provider completion has no finish reason")
		}
		toolIndices := slices.Sorted(maps.Keys(calls[index]))
		for _, toolIndex := range toolIndices {
			acc := calls[index][toolIndex]
			call := acc.call
			call.Function.Name, call.Function.Arguments = acc.name.String(), acc.arguments.String()
			if call.Id == "" || call.Function.Name == "" {
				return nil, fmt.Errorf("provider returned incomplete tool call")
			}
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, call)
		}
		out.Choices = append(out.Choices, choice)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("provider returned no choices")
	}
	return out, nil
}

// HTTPError never includes the provider body: it can echo prompts or secrets.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return fmt.Sprintf("provider HTTP status %d", e.StatusCode) }

func DoRequest(ctx context.Context, endpoint, apiKey string, body map[string]any) ([]byte, error) {
	response, err := doRequestStream(ctx, endpoint, apiKey, body)
	if err != nil {
		return nil, err
	}
	defer response.Close()
	result, err := io.ReadAll(response)
	if err != nil {
		return nil, fmt.Errorf("provider response read failed: %w", err)
	}
	return result, nil
}

func doRequestStream(ctx context.Context, endpoint, apiKey string, body map[string]any) (io.ReadCloser, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode provider request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("invalid provider endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("provider transport failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, &HTTPError{StatusCode: response.StatusCode}
	}
	return response.Body, nil
}
