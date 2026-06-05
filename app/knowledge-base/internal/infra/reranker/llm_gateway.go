package reranker

import (
	"context"
	"sort"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/zrpc"
)

type LLMGatewayReranker struct {
	client zrpc.Client
}

func NewLLMGatewayReranker(client zrpc.Client) domain.Reranker {
	return &LLMGatewayReranker{client: client}
}

func (r *LLMGatewayReranker) Rerank(ctx context.Context, req *domain.RerankRequest) ([]domain.RerankResult, error) {
	texts := make([]string, len(req.Candidates))
	for i, c := range req.Candidates {
		texts[i] = c.Content
	}

	conn := r.client.Conn()
	if conn == nil {
		return nil, errors.New(errors.CodeRPCError, "llm-gateway connection not available")
	}

	cli := pb.NewLLMGatewayClient(conn)
	pbReq := &pb.RerankReq{
		ModelId:         req.ModelID,
		OwnerId:         req.OwnerID,
		Query:           req.Query,
		Documents:       texts,
		TopN:            int32(req.TopK),
		ReturnDocuments: false,
	}

	resp, err := cli.Rerank(ctx, pbReq)
	if err != nil {
		return nil, errors.Wrap(errors.CodeRPCError, "rerank request failed", err)
	}

	results := make([]domain.RerankResult, len(resp.Results))
	for i, rr := range resp.Results {
		results[i] = domain.RerankResult{
			Index: int(rr.Index),
			Score: rr.RelevanceScore,
		}
		if int(rr.Index) < len(req.Candidates) {
			results[i].Content = req.Candidates[rr.Index].Content
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}
