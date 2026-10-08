package client

import (
	"context"

	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// LlmGatewayClient keeps the exact request/response trajectory owned by runtime.
type LlmGatewayClient struct {
	cli llmgateway.LLMGatewayClient
}

func NewLlmGatewayClient(c zrpc.Client) *LlmGatewayClient {
	return &LlmGatewayClient{cli: llmgateway.NewLLMGatewayClient(c.Conn())}
}

func (c *LlmGatewayClient) SnapshotModel(ctx context.Context, modelID string, ownerID *string, botID string) (*llmgateway.ModelExecutionConfig, error) {
	return c.cli.ResolveExecutionModel(serviceCallContext(ctx), &llmgateway.ResolveExecutionModelReq{ModelId: modelID, OwnerId: ownerID, BotId: optionalID(botID)})
}

func (c *LlmGatewayClient) ChatRequest(ctx context.Context, req *llmgateway.ChatReq) (*llmgateway.ChatResp, error) {
	return c.cli.Chat(serviceCallContext(ctx), req)
}

func (c *LlmGatewayClient) ChatRequestStream(ctx context.Context, req *llmgateway.ChatReq) (grpc.ServerStreamingClient[llmgateway.ChatStreamChunk], error) {
	return c.cli.ChatStream(serviceCallContext(ctx), req)
}

func optionalID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// Bot execution is a domain-to-domain call. The triggering member is not the
// billing owner and must not be forwarded as the request's authenticated user.
func serviceCallContext(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("user-id")
	md.Delete("x-user-id")
	return metadata.NewOutgoingContext(ctx, md)
}
