package client

import (
	"context"
	"encoding/json"
	"io"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc/metadata"
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
func (c *LlmGatewayClient) Chat(ctx context.Context, modelID string, modelName string, messages []*schema.Message, sysPrompt string, ownerID *string, toolInfo []*schema.ToolInfo, botID ...string) (*schema.Message, error) {
	req := &llmgateway.ChatReq{
		ModelId: modelID,
		OwnerId: ownerID,
		BotId:   callBotID(botID),
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
	resp, err := c.cli.Chat(serviceCallContext(ctx), req)
	if err != nil {
		return nil, err
	}
	return chatRespToSchema(resp), nil
}

// ChatStream sends a streaming chat request.
func (c *LlmGatewayClient) ChatStream(ctx context.Context, modelID string, modelName string, messages []*schema.Message, sysPrompt string, ownerID *string, toolInfo []*schema.ToolInfo, botID ...string) (*schema.StreamReader[*schema.Message], error) {
	req := &llmgateway.ChatReq{
		ModelId: modelID,
		OwnerId: ownerID,
		BotId:   callBotID(botID),
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
	stream, err := c.cli.ChatStream(serviceCallContext(ctx), req)
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
func (c *LlmGatewayClient) Embed(ctx context.Context, modelID string, texts []string, ownerID *string, botID ...string) ([][]float32, error) {
	req := &llmgateway.EmbedReq{ModelId: modelID, Input: texts, OwnerId: ownerID, BotId: callBotID(botID)}
	resp, err := c.cli.Embed(serviceCallContext(ctx), req)
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
	modelID  string
	botID    string
	ownerID  *string
	model    string
	toolInfo []*schema.ToolInfo
}

// NewEinoChatModel creates an Eino ChatModel backed by llm-gateway gRPC.
func (c *LlmGatewayClient) NewEinoChatModel(modelID string, modelName string, ownerID *string, botID ...string) *EinoChatModel {
	return &EinoChatModel{
		client:  c,
		modelID: modelID,
		model:   modelName,
		ownerID: ownerID,
		botID:   firstBotID(botID),
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
	return m.client.Chat(ctx, m.modelID, m.model, filtered, sysPrompt, m.ownerID, m.toolInfo, m.botID)
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
	return m.client.ChatStream(ctx, m.modelID, m.model, filtered, sysPrompt, m.ownerID, m.toolInfo, m.botID)
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

func optionalID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func firstBotID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func callBotID(ids []string) *string {
	return optionalID(firstBotID(ids))
}

// Bot execution is a domain-to-domain call. The triggering member is not the
// billing owner and must not be forwarded as the LLM request's authenticated user.
func serviceCallContext(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("user-id")
	md.Delete("x-user-id")
	return metadata.NewOutgoingContext(ctx, md)
}
