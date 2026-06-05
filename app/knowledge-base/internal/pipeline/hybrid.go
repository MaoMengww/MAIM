package pipeline

import (
	"context"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

type HybridSearcher struct {
	vectorStore domain.VectorStore
}

func NewHybridSearcher(vs domain.VectorStore) *HybridSearcher {
	return &HybridSearcher{vectorStore: vs}
}

func (s *HybridSearcher) Search(ctx context.Context, queryVector []float32, query string, topK int, filter domain.SearchFilter, cfg domain.RetrievalConfig) ([]domain.SearchResult, error) {
	return s.vectorStore.HybridSearch(ctx, queryVector, query, topK, filter, domain.WeightConfig{
		DenseWeight:  cfg.DenseWeight,
		SparseWeight: cfg.SparseWeight,
	})
}
