package domain

import "context"

type RerankRequest struct {
	ModelID    int64
	OwnerID    int64
	Query      string
	Candidates []RerankCandidate
	TopK       int
}

type RerankCandidate struct {
	Index   int
	Content string
}

type RerankResult struct {
	Index   int
	Score   float32
	Content string
}

type Reranker interface {
	Rerank(ctx context.Context, req *RerankRequest) ([]RerankResult, error)
}
