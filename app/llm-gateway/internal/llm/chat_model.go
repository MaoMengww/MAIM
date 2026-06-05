package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/pkg/logx"
)

func NewChatModel(modelName, provider, apiKey, baseURL string) model.BaseChatModel {
	if baseURL == "" {
		switch provider {
		case "openai":
			baseURL = "https://api.openai.com"
		case "deepseek":
			baseURL = "https://api.deepseek.com"
		case "qwen":
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode"
		}
	}
	return &chatModel{model: modelName, apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

type chatModel struct {
	model   string
	apiKey  string
	baseURL string
}

var (
	_ model.BaseChatModel = (*chatModel)(nil)
	_ components.Checker  = (*chatModel)(nil)
)

func (m *chatModel) IsCallbacksEnabled() bool { return true }
func (m *chatModel) GetType() string          { return "EinoChatModel" }

func (m *chatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	commonOpts := model.GetCommonOptions(nil, opts...)

	logger := logx.DefaultLogger().WithContext(ctx)

	ctx = callbacks.OnStart(ctx, &model.CallbackInput{
		Messages: in,
		Tools:    commonOpts.Tools,
	})

	respBytes, err := DoRequest(ctx, m.baseURL+"/chat/completions", m.apiKey, buildBody(m.model, in, commonOpts, false))
	if err != nil {
		logger.Errorf("model=%s generate error: %v", m.model, err)
		callbacks.OnError(ctx, err)
		return nil, err
	}

	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		callbacks.OnError(ctx, err)
		return nil, fmt.Errorf("decode response: %w", err)
	}

	out := &schema.Message{Role: schema.Assistant}
	if len(result.Choices) > 0 {
		c := result.Choices[0]
		out.Content = c.Message.Content
		for _, tc := range c.Message.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
				ID: tc.ID, Type: tc.Type,
				Function: schema.FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
		out.ResponseMeta = &schema.ResponseMeta{FinishReason: c.FinishReason}
		if result.Usage != nil {
			out.ResponseMeta.Usage = &schema.TokenUsage{
				PromptTokens:     result.Usage.PromptTokens,
				CompletionTokens: result.Usage.CompletionTokens,
				TotalTokens:      result.Usage.TotalTokens,
			}
		}
	}

	tokenUsage := &model.TokenUsage{}
	if result.Usage != nil {
		tokenUsage.PromptTokens = result.Usage.PromptTokens
		tokenUsage.CompletionTokens = result.Usage.CompletionTokens
		tokenUsage.TotalTokens = result.Usage.TotalTokens
		logger.Infof("model=%s input_tokens=%d output_tokens=%d total_tokens=%d",
			m.model, result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
	} else {
		logger.Infof("model=%s generated (no token usage reported)", m.model)
	}

	callbacks.OnEnd(ctx, &model.CallbackOutput{Message: out, TokenUsage: tokenUsage})
	return out, nil
}

func (m *chatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	commonOpts := model.GetCommonOptions(nil, opts...)

	ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: in})

	logger := logx.DefaultLogger().WithContext(ctx)

	respBody, err := doRequestStream(ctx, m.baseURL+"/chat/completions", m.apiKey, buildBody(m.model, in, commonOpts, true))
	if err != nil {
		logger.Errorf("model=%s stream error: %v", m.model, err)
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](64)

	go func() {
		defer sw.Close()
		defer respBody.Close()

		scanner := bufio.NewScanner(respBody)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var lastUsage *model.TokenUsage

		// Accumulate streaming tool call deltas by index (OpenAI incremental format)
		type tcAccum struct {
			id, typ, name string
			args          strings.Builder
		}
		accByIndex := map[int]*tcAccum{}

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || line == "data: [DONE]" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			// Accumulate tool call deltas by index across chunks
			if len(chunk.Choices) > 0 {
				for _, tc := range chunk.Choices[0].Delta.ToolCalls {
					acc, ok := accByIndex[tc.Index]
					if !ok {
						acc = &tcAccum{}
						accByIndex[tc.Index] = acc
					}
					if tc.ID != "" {
						acc.id = tc.ID
					}
					if tc.Type != "" {
						acc.typ = tc.Type
					}
					if tc.Function.Name != "" {
						acc.name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						acc.args.WriteString(tc.Function.Arguments)
					}
				}
			}

			msg := &schema.Message{Role: schema.Assistant}
			if len(chunk.Choices) > 0 {
				c := chunk.Choices[0]
				msg.Content = c.Delta.Content
				if c.FinishReason != "" {
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: c.FinishReason}
					// On finish_reason="tool_calls", emit complete accumulated ToolCalls
					if c.FinishReason == "tool_calls" && len(accByIndex) > 0 {
						for _, acc := range accByIndex {
							if acc.id == "" {
								continue
							}
							msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
								ID:   acc.id,
								Type: acc.typ,
								Function: schema.FunctionCall{
									Name:      acc.name,
									Arguments: acc.args.String(),
								},
							})
						}
					}
				}
			}
			if chunk.Usage != nil {
				lastUsage = &model.TokenUsage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}
			sw.Send(msg, nil)
		}

		if lastUsage != nil {
			logger.Infof("model=%s input_tokens=%d output_tokens=%d total_tokens=%d",
				m.model, lastUsage.PromptTokens, lastUsage.CompletionTokens, lastUsage.TotalTokens)
			callbacks.OnEnd(ctx, &model.CallbackOutput{TokenUsage: lastUsage})
		} else {
			logger.Infof("model=%s stream completed (no token usage reported)", m.model)
		}
	}()

	return sr, nil
}

// --- package-level helpers ---

func buildBody(modelName string, msgs []*schema.Message, opts *model.Options, stream bool) map[string]any {
	body := map[string]any{"model": modelName, "messages": messagesToMap(msgs)}
	if stream {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if opts == nil {
		return body
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if opts.MaxTokens != nil {
		body["max_tokens"] = *opts.MaxTokens
	}
	if opts.TopP != nil {
		body["top_p"] = *opts.TopP
	}
	if len(opts.Stop) > 0 {
		body["stop"] = opts.Stop
	}
	if len(opts.Tools) > 0 {
		tools := make([]map[string]any, len(opts.Tools))
		for i, t := range opts.Tools {
			fn := map[string]any{"name": t.Name, "description": t.Desc, "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}
			if t.ParamsOneOf != nil {
				js, err := t.ParamsOneOf.ToJSONSchema()
				if err == nil && js != nil {
					if b, err := json.Marshal(js); err == nil && string(b) != "{}" {
						fn["parameters"] = json.RawMessage(b)
					}
				}
			}
			tools[i] = map[string]any{"type": "function", "function": fn}
		}
		body["tools"] = tools
	}
	return body
}

func DoRequest(ctx context.Context, url, apiKey string, body map[string]any) ([]byte, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	b, _ = io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}

func doRequestStream(ctx context.Context, url, apiKey string, body map[string]any) (io.ReadCloser, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(b))
	}
	return resp.Body, nil
}

func messagesToMap(msgs []*schema.Message) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		e := map[string]any{"role": string(m.Role)}
		if m.Content != "" {
			e["content"] = m.Content
		}
		if m.Name != "" {
			e["name"] = m.Name
		}
		if m.ToolCallID != "" {
			e["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]any, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcs[j] = map[string]any{
					"id": tc.ID, "type": tc.Type,
					"function": map[string]string{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
				}
			}
			e["tool_calls"] = tcs
		}
		out[i] = e
	}
	return out
}
