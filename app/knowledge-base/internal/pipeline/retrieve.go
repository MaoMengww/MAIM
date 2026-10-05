package pipeline

import (
	"context"
	"strconv"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/logx"
)

type RetrievePipeline struct {
	KBRepo           domain.KBRepo
	Embedder         domain.Embedder
	VectorStore      domain.VectorStore
	Reranker         domain.Reranker
	Logger           logx.Logger
	EmbeddingModelID int64
}

func (p *RetrievePipeline) Retrieve(ctx context.Context, kbIDs []int64, query string, retrievalCfg domain.RetrievalConfig, embeddingModelID, ownerID int64) ([]domain.RetrieveItem, error) {
	logger := p.Logger.WithContext(ctx)
	if len(kbIDs) == 0 {
		return nil, nil
	}
	if retrievalCfg.TopK <= 0 {
		retrievalCfg.TopK = 5
	}
	if retrievalCfg.CandidateTopK <= 0 {
		retrievalCfg.CandidateTopK = max(20, retrievalCfg.TopK)
	}
	// 1. Query embedding
	vectors, err := p.Embedder.Embed(ctx, []string{query}, embeddingModelID, ownerID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeRPCError, "query embedding failed", err)
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	queryVector := vectors[0]

	// 2. Search based on mode
	var results []domain.SearchResult
	switch retrievalCfg.Mode {
	case "vector":
		results, err = p.VectorStore.Search(ctx, queryVector, retrievalCfg.CandidateTopK, domain.SearchFilter{
			KBIDs:          kbIDs,
			ScoreThreshold: retrievalCfg.ScoreThreshold,
		})
	case "fulltext":
		results, err = p.VectorStore.SparseSearch(ctx, query, retrievalCfg.CandidateTopK, domain.SearchFilter{
			KBIDs:          kbIDs,
			ScoreThreshold: retrievalCfg.ScoreThreshold,
		})
	default: // hybrid
		results, err = p.VectorStore.HybridSearch(ctx, queryVector, query, retrievalCfg.CandidateTopK, domain.SearchFilter{
			KBIDs:          kbIDs,
			ScoreThreshold: retrievalCfg.ScoreThreshold,
		}, domain.WeightConfig{
			DenseWeight:  retrievalCfg.DenseWeight,
			SparseWeight: retrievalCfg.SparseWeight,
		})
	}
	if err != nil {
		logger.Errorf("search failed: %v", err)
		return nil, errors.Wrap(errors.CodeInternal, "search failed", err)
	}

	// 3. Truncate to CandidateTopK
	if len(results) > retrievalCfg.CandidateTopK {
		results = results[:retrievalCfg.CandidateTopK]
	}

	// 4. Optional Rerank
	if retrievalCfg.Rerank.Enabled && p.Reranker != nil && len(results) > retrievalCfg.TopK {
		candidates := make([]domain.RerankCandidate, len(results))
		for i, r := range results {
			candidates[i] = domain.RerankCandidate{Index: i, Content: r.Content}
		}
		reranked, err := p.Reranker.Rerank(ctx, &domain.RerankRequest{
			ModelID:    retrievalCfg.Rerank.ModelID,
			OwnerID:    ownerID,
			Query:      query,
			Candidates: candidates,
			TopK:       retrievalCfg.Rerank.TopN,
		})
		if err == nil {
			rerankedResults := make([]domain.SearchResult, 0, len(reranked))
			for _, rr := range reranked {
				if rr.Index < len(results) {
					rerankedResults = append(rerankedResults, results[rr.Index])
				}
			}
			results = rerankedResults
		}
		// On rerank failure, keep original results
	}

	// 5. Truncate to TopK
	if len(results) > retrievalCfg.TopK {
		results = results[:retrievalCfg.TopK]
	}

	// 6. Score threshold filter
	filtered := make([]domain.SearchResult, 0, len(results))
	for _, r := range results {
		if r.Score >= retrievalCfg.ScoreThreshold {
			filtered = append(filtered, r)
		}
	}
	results = filtered

	// 7. Parent-child expand
	results, err = p.expandParentChunks(ctx, results)
	if err != nil {
		logger.Errorf("expand parent chunks failed: %v", err)
	}

	// 8. Convert to RetrieveItem
	items := make([]domain.RetrieveItem, 0, len(results))
	for _, r := range results {
		docTitle, _ := r.Metadata["doc_title"].(string)
		docIDText, _ := r.Metadata["doc_id"].(string)
		docID, _ := strconv.ParseInt(docIDText, 10, 64)
		items = append(items, domain.RetrieveItem{
			Content:        r.Content,
			Score:          r.Score,
			DocID:          docID,
			KBID:           r.KBID,
			DocTitle:       docTitle,
			MatchedContent: r.Content,
			Metadata:       r.Metadata,
		})
	}

	return items, nil
}

func (p *RetrievePipeline) expandParentChunks(ctx context.Context, results []domain.SearchResult) ([]domain.SearchResult, error) {
	// For now, return children results directly. A complete implementation
	// would look up parent chunks from the database using KBID and ChunkIndex.
	return results, nil
}
