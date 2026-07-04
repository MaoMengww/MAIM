package memory

import (
	"context"
	"fmt"

	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/zeromicro/go-zero/zrpc"
)

// Embedder turns text into vectors using llm-gateway.
type Embedder interface {
	Embed(ctx context.Context, texts []string, modelID int64, ownerID int64) ([][]float32, error)
}

// GatewayEmbedder calls llm-gateway's Embed RPC.
type GatewayEmbedder struct {
	client zrpc.Client
}

// NewGatewayEmbedder creates an embedder backed by the llm-gateway connection.
func NewGatewayEmbedder(client zrpc.Client) Embedder {
	return &GatewayEmbedder{client: client}
}

func (e *GatewayEmbedder) Embed(ctx context.Context, texts []string, modelID int64, ownerID int64) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if modelID <= 0 {
		return nil, fmt.Errorf("embedding model_id is required")
	}

	conn := e.client.Conn()
	if conn == nil {
		return nil, fmt.Errorf("llm-gateway connection not available")
	}

	cli := llmgateway.NewLLMGatewayClient(conn)
	req := &llmgateway.EmbedReq{
		ModelId: modelID,
		Input:   texts,
		OwnerId: ownerID,
	}

	resp, err := cli.Embed(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}

	results := make([][]float32, len(resp.Data))
	for _, d := range resp.Data {
		if int(d.Index) < len(results) {
			results[d.Index] = d.Embedding
		}
	}
	return results, nil
}
