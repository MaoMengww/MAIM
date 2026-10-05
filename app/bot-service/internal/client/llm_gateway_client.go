package client

import (
	"context"
	"encoding/json"
	"io"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/zeromicro/go-zero/zrpc"
)

// LlmGatewayClient wraps llm-gateway gRPC.
type LlmGatewayClient struct {
	cli llmgateway.LLMGatewayClient
}

// NewLlmGatewayClient creates a new llm-gateway client wrapper.
func NewLlmGatewayClient(c zrpc.Client) *LlmGatewayClient {
	return &LlmGatewayClient{
		cli: llmgateway.NewLLMGatewayClient(c.Conn()),
	}
}

// Chat sends a chat request with model_id as the primary routing key.
func (c *LlmGatewayClient) Chat(ctx context.Context, modelID int64, modelName string, messages []*schema.Message, sysPrompt string, ownerID int64, toolInfo []*schema.ToolInfo) (*schema.Message, error) {
	req := &llmgateway.ChatReq{
		ModelId: modelID,
		OwnerId: ownerID,
	}
	if sysPrompt != "" {
		req.Messages = append(req.Messages, &llmgateway.Message{Role: "system", Content: sysPrompt})
	}
	for _, m := range messages {
		req.Messages = append(req.Messages, schemaMsgToProto(m))
	}
	if len(toolInfo) > 0 {
		req.Tools = toolInfoToProto(toolInfo)
	}
	resp, err := c.cli.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return chatRespToSchema(resp), nil
}

// ChatStream sends a streaming chat request.
func (c *LlmGatewayClient) ChatStream(ctx context.Context, modelID int64, modelName string, messages []*schema.Message, sysPrompt string, ownerID int64, toolInfo []*schema.ToolInfo) (*schema.StreamReader[*schema.Message], error) {
	req := &llmgateway.ChatReq{
		ModelId: modelID,
		OwnerId: ownerID,
	}
	if sysPrompt != "" {
		req.Messages = append(req.Messages, &llmgateway.Message{Role: "system", Content: sysPrompt})
	}
	for _, m := range messages {
		req.Messages = append(req.Messages, schemaMsgToProto(m))
	}
	if len(toolInfo) > 0 {
		req.Tools = toolInfoToProto(toolInfo)
	}
	stream, err := c.cli.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}

	reader, writer := schema.Pipe[*schema.Message](0)
	go func() {
		defer writer.Close()
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = writer.Send(nil, err)
				return
			}
			msg := streamChunkToSchema(chunk)
			if msg != nil {
				_ = writer.Send(msg, nil)
			}
		}
	}()
	return reader, nil
}

// Embed calls llm-gateway Embed.
func (c *LlmGatewayClient) Embed(ctx context.Context, modelID int64, texts []string, ownerID int64) ([][]float32, error) {
	req := &llmgateway.EmbedReq{ModelId: modelID, Input: texts, OwnerId: ownerID}
	resp, err := c.cli.Embed(ctx, req)
	if err != nil {
		return nil, err
	}
	result := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		result[i] = d.Embedding
	}
	return result, nil
}

// --- helpers ---

func schemaMsgToProto(m *schema.Message) *llmgateway.Message {
	pm := &llmgateway.Message{
		Role:       string(m.Role),
		Content:    m.Content,
		Name:       m.Name,
		ToolCallId: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		pm.ToolCalls = append(pm.ToolCalls, &llmgateway.ToolCall{
			Id:   tc.ID,
			Type: tc.Type,
			Function: &llmgateway.ToolCall_Function{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return pm
}

func chatRespToSchema(resp *llmgateway.ChatResp) *schema.Message {
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "",
	}
	if len(resp.Choices) > 0 {
		msg.Content = resp.Choices[0].Message.Content
		for _, tc := range resp.Choices[0].Message.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   tc.Id,
				Type: tc.Type,
				Function: schema.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}
	if resp.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     int(resp.Usage.PromptTokens),
				CompletionTokens: int(resp.Usage.CompletionTokens),
			},
		}
	}
	return msg
}

func streamChunkToSchema(chunk *llmgateway.ChatStreamChunk) *schema.Message {
	if len(chunk.Choices) == 0 {
		return nil
	}
	c := chunk.Choices[0]
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: c.Delta.Content,
	}
	for _, tc := range c.Delta.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.Id,
			Type: tc.Type,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	if chunk.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     int(chunk.Usage.PromptTokens),
				CompletionTokens: int(chunk.Usage.CompletionTokens),
			},
		}
	}
	return msg
}

// EinoChatModel wraps LlmGatewayClient as an Eino model.ChatModel.
type EinoChatModel struct {
	client   *LlmGatewayClient
	modelID  int64
	botID    int64
	ownerID  int64
	model    string
	toolInfo []*schema.ToolInfo
}

// NewEinoChatModel creates an Eino ChatModel backed by llm-gateway gRPC.
func (c *LlmGatewayClient) NewEinoChatModel(modelID int64, modelName string, ownerID int64) *EinoChatModel {
	return &EinoChatModel{
		client:  c,
		modelID: modelID,
		model:   modelName,
		ownerID: ownerID,
	}
}

func (m *EinoChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
	var sysPrompt string
	filtered := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == schema.System {
			if sysPrompt == "" {
				sysPrompt = msg.Content
			}
		} else {
			filtered = append(filtered, msg)
		}
	}
	return m.client.Chat(ctx, m.modelID, m.model, filtered, sysPrompt, m.ownerID, m.toolInfo)
}

func (m *EinoChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	var sysPrompt string
	filtered := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == schema.System {
			if sysPrompt == "" {
				sysPrompt = msg.Content
			}
		} else {
			filtered = append(filtered, msg)
		}
	}
	return m.client.ChatStream(ctx, m.modelID, m.model, filtered, sysPrompt, m.ownerID, m.toolInfo)
}

func (m *EinoChatModel) WithTools(tools []*schema.ToolInfo) (einoModel.ToolCallingChatModel, error) {
	m.toolInfo = append(m.toolInfo, tools...)
	return m, nil
}

func (m *EinoChatModel) BindTools(tools []*schema.ToolInfo) error {
	m.toolInfo = append(m.toolInfo, tools...)
	return nil
}

// toolInfoToProto converts eino ToolInfo definitions to llm-gateway ToolDef proto slice.
func toolInfoToProto(tis []*schema.ToolInfo) []*llmgateway.ToolDef {
	protos := make([]*llmgateway.ToolDef, 0, len(tis))
	for _, ti := range tis {
		t := &llmgateway.ToolDef{
			Name:        ti.Name,
			Description: ti.Desc,
		}
		if ti.ParamsOneOf != nil {
			js, err := ti.ParamsOneOf.ToJSONSchema()
			if err == nil && js != nil {
				if b, err := json.Marshal(js); err == nil && string(b) != "{}" {
					t.Parameters = b
				}
			}
		}
		protos = append(protos, t)
	}
	return protos
}

// Ensure EinoChatModel implements the required interfaces.
var _ einoModel.BaseChatModel = (*EinoChatModel)(nil)
var _ einoModel.ChatModel = (*EinoChatModel)(nil)
