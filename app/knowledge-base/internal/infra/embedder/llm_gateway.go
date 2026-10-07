package embedder

import (
	"context"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/zeromicro/go-zero/zrpc"
)

type LLMGatewayEmbedder struct {
	client zrpc.Client
}

func NewLLMGatewayEmbedder(client zrpc.Client) domain.Embedder {
	return &LLMGatewayEmbedder{client: client}
}

func (e *LLMGatewayEmbedder) Embed(ctx context.Context, texts []string, modelID string, ownerID *string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if err := identity.Validate(modelID); err != nil {
		return nil, errors.Wrap(errors.CodeInvalidParam, "invalid embedding model_id", err)
	}
	if ownerID != nil {
		if err := identity.Validate(*ownerID); err != nil {
			return nil, errors.Wrap(errors.CodeInvalidParam, "invalid embedding owner_id", err)
		}
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

func (e *LLMGatewayEmbedder) Dimensions(modelID string) int {
	return 1536
}
