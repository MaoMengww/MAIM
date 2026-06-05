package llmgateway

import (
	"context"
	"io"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/zeromicro/go-zero/zrpc"
)

type GatewayAdapter struct {
	cli zrpc.Client
}

func NewGatewayAdapter(cli zrpc.Client) *GatewayAdapter {
	return &GatewayAdapter{cli: cli}
}

func (a *GatewayAdapter) Chat(ctx context.Context, req *pipeline.LLMChatRequest) (*pipeline.LLMChatResponse, error) {
	messages := make([]*pb.Message, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = &pb.Message{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	chatReq := &pb.ChatReq{
		ModelId:     req.ModelId,
		OwnerId:     req.OwnerID,
		Messages:    messages,
		Temperature: 0.3,
	}

	conn := a.cli.Conn()
	client := pb.NewLLMGatewayClient(conn)
	resp, err := client.Chat(ctx, chatReq)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return &pipeline.LLMChatResponse{Content: ""}, nil
	}
	return &pipeline.LLMChatResponse{
		Content: resp.Choices[0].Message.Content,
		Model:   resp.Model,
	}, nil
}

// EinoChatModel wraps llm-gateway gRPC as an eino BaseChatModel with tool support.
type EinoChatModel struct {
	client  pb.LLMGatewayClient
	modelID int64
	ownerID int64
}

func NewEinoChatModel(cli pb.LLMGatewayClient, modelID, ownerID int64) *EinoChatModel {
	return &EinoChatModel{client: cli, modelID: modelID, ownerID: ownerID}
}

func (m *EinoChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	req := &pb.ChatReq{
		ModelId:  m.modelID,
		OwnerId:  m.ownerID,
		Messages: toProtoMessages(messages),
	}
	resp, err := m.client.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return toSchemaMessage(resp), nil
}

func (m *EinoChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	req := &pb.ChatReq{
		ModelId:  m.modelID,
		OwnerId:  m.ownerID,
		Messages: toProtoMessages(messages),
	}
	stream, err := m.client.ChatStream(ctx, req)
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

func (m *EinoChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *EinoChatModel) BindTools(tools []*schema.ToolInfo) error {
	return nil
}

// -- helpers --

func toProtoMessages(msgs []*schema.Message) []*pb.Message {
	out := make([]*pb.Message, 0, len(msgs))
	for _, msg := range msgs {
		p := &pb.Message{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCallId: msg.ToolCallID,
		}
		for _, tc := range msg.ToolCalls {
			p.ToolCalls = append(p.ToolCalls, &pb.ToolCall{
				Id:   tc.ID,
				Type: tc.Type,
				Function: &pb.ToolCall_Function{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out = append(out, p)
	}
	return out
}

func toSchemaMessage(resp *pb.ChatResp) *schema.Message {
	msg := &schema.Message{Role: schema.Assistant}
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

func streamChunkToSchema(chunk *pb.ChatStreamChunk) *schema.Message {
	if len(chunk.Choices) == 0 {
		return nil
	}
	c := chunk.Choices[0]
	msg := &schema.Message{Role: schema.Assistant, Content: c.Delta.Content}
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
	return msg
}

var _ model.BaseChatModel = (*EinoChatModel)(nil)
