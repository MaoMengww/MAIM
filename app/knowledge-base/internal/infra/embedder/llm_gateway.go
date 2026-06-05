package embedder

import (
	"context"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/zrpc"
)

type LLMGatewayEmbedder struct {
	client zrpc.Client
}

func NewLLMGatewayEmbedder(client zrpc.Client) domain.Embedder {
	return &LLMGatewayEmbedder{client: client}
}

func (e *LLMGatewayEmbedder) Embed(ctx context.Context, texts []string, modelID int64, ownerID int64) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if modelID <= 0 {
		return nil, errors.New(errors.CodeRPCError, "model_id is required for embedding")
	}

	conn := e.client.Conn()
	if conn == nil {
		return nil, errors.New(errors.CodeRPCError, "llm-gateway connection not available")
	}

	cli := llmgateway.NewLLMGatewayClient(conn)
	req := &llmgateway.EmbedReq{
		ModelId: modelID,
		Input:   texts,
		OwnerId: ownerID,
	}

	resp, err := cli.Embed(ctx, req)
	if err != nil {
		return nil, errors.Wrap(errors.CodeRPCError, "embedding request failed", err)
	}

	results := make([][]float32, len(resp.Data))
	for _, d := range resp.Data {
		if int(d.Index) < len(results) {
			results[d.Index] = d.Embedding
		}
	}
	return results, nil
}

func (e *LLMGatewayEmbedder) Dimensions(modelID int64) int {
	return 1536
}
