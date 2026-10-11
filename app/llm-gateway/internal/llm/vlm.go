package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

// CompleteVLMChat uses Eino's non-streaming multimodal component. The response
// modifier preserves usage presence, which the SDK's value-typed usage loses.
func CompleteVLMChat(ctx context.Context, req *pb.VlmChatReq, entry *domain.ModelEntry) (*pb.ChatResp, error) {
	baseURL, err := ChatBaseURL(entry.Provider, entry.BaseURL)
	if err != nil {
		return nil, err
	}
	messages := make([]*schema.Message, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, schema.SystemMessage(req.SystemPrompt))
	}
	messages = append(messages, &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{Type: schema.ChatMessagePartTypeText, Text: req.UserPrompt},
			{Type: schema.ChatMessagePartTypeImageURL, Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{URL: &req.ImageData},
			}},
		},
	})
	bridge := &vlmTransport{}
	component, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey: entry.APIKey, Model: entry.ModelName, BaseURL: baseURL,
		HTTPClient: &http.Client{Transport: bridge, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	})
	if err != nil {
		return nil, fmt.Errorf("provider component initialization failed")
	}
	var usage *providerUsage
	opts := []model.Option{openai.WithResponseMessageModifier(func(_ context.Context, msg *schema.Message, rawBody []byte) (*schema.Message, error) {
		var wire struct {
			Usage *providerUsage `json:"usage"`
		}
		if err := json.Unmarshal(rawBody, &wire); err != nil {
			return nil, fmt.Errorf("invalid provider response")
		}
		usage = wire.Usage
		if usage == nil && msg.ResponseMeta != nil {
			msg.ResponseMeta.Usage = nil
		}
		return msg, nil
	})}
	// Preserve the existing VLM contract: zero/negative temperature is omitted.
	// ExtraFields retains the protobuf's float64 rather than narrowing to float32.
	if req.Temperature > 0 {
		opts = append(opts, openai.WithExtraFields(map[string]any{"temperature": req.Temperature}))
	}
	if req.MaxTokens > 0 {
		opts = append(opts, model.WithMaxTokens(int(req.MaxTokens)))
	}
	message, err := component.Generate(ctx, messages, opts...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if bridge.err != nil {
			return nil, bridge.err
		}
		// SDK errors may contain response bodies, prompts or credential-bearing URLs.
		return nil, fmt.Errorf("provider completion failed")
	}
	out := &pb.ChatResp{
		Model: entry.ModelName,
		Choices: []*pb.ChatResp_Choice{{
			Index:   0,
			Message: &pb.Message{Role: "assistant", Content: message.Content},
		}},
	}
	if usage != nil {
		out.Usage = &pb.UsageInfo{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
		}
	}
	if message.ResponseMeta != nil {
		out.Choices[0].FinishReason = message.ResponseMeta.FinishReason
	}
	return out, nil
}

// vlmTransport leaves request rendering and response decoding to Eino. It only
// blocks unsafe status bodies from reaching SDK errors, just like chatWireBridge.
type vlmTransport struct{ err error }

func (t *vlmTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.err = fmt.Errorf("provider transport failed")
		return nil, t.err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		t.err = &HTTPError{StatusCode: response.StatusCode}
		return nil, t.err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	_ = response.Body.Close()
	if err != nil {
		t.err = fmt.Errorf("provider response read failed")
		return nil, t.err
	}
	if len(data) > maxProviderResponseBytes {
		t.err = fmt.Errorf("provider response too large")
		return nil, t.err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
