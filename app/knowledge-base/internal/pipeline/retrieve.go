package pipeline

import (
	"context"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/logx"
)

type RetrievePipeline struct {
	KBRepo      domain.KBRepo
	DocRepo     domain.DocumentRepo
	Embedder    domain.Embedder
	VectorStore domain.VectorStore
	Reranker    domain.Reranker
	Logger      logx.Logger
}

func (p *RetrievePipeline) Retrieve(ctx context.Context, kbIDs []string, query string, retrievalCfg domain.RetrievalConfig, embeddingModelID string, ownerID *string) ([]domain.RetrieveItem, error) {
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
	vectors, err := p.Embedder.Embed(ctx, []string{query}, embeddingModelID, ownerID)
	if err != nil {
		return nil, errors.Wrap(errors.CodeRPCError, "query embedding failed", err)
	}
	if len(vectors) == 0 {
		return nil, nil
	}

	var results []domain.SearchResult
	filter := domain.SearchFilter{KBIDs: kbIDs, ScoreThreshold: retrievalCfg.ScoreThreshold}
	switch retrievalCfg.Mode {
	case "vector":
		results, err = p.VectorStore.Search(ctx, vectors[0], retrievalCfg.CandidateTopK, filter)
	case "fulltext":
		results, err = p.VectorStore.SparseSearch(ctx, query, retrievalCfg.CandidateTopK, filter)
	default:
		results, err = p.VectorStore.HybridSearch(ctx, vectors[0], query, retrievalCfg.CandidateTopK, filter, domain.WeightConfig{
			DenseWeight: retrievalCfg.DenseWeight, SparseWeight: retrievalCfg.SparseWeight,
		})
	}
	if err != nil {
		logger.Errorf("search failed: %v", err)
		return nil, errors.Wrap(errors.CodeInternal, "search failed", err)
	}
	if len(results) > retrievalCfg.CandidateTopK {
		results = results[:retrievalCfg.CandidateTopK]
	}
	filtered := make([]domain.SearchResult, 0, len(results))
	for _, result := range results {
		if result.Score >= retrievalCfg.ScoreThreshold {
			filtered = append(filtered, result)
		}
	}
	results = filtered

	// The relationship store is authoritative: stale vectors from replacement,
	// failure or deletion must not occupy result slots or reach the reranker.
	results, chunks, err := p.currentChunks(ctx, kbIDs, results)
	if err != nil {
		return nil, err
	}
	if retrievalCfg.Rerank.Enabled && p.Reranker != nil && len(results) > retrievalCfg.TopK {
		if retrievalCfg.Rerank.ModelID == nil {
			return nil, errors.New(errors.CodeInvalidParam, "rerank model is not configured")
		}
		candidates := make([]domain.RerankCandidate, len(results))
		for i, result := range results {
			candidates[i] = domain.RerankCandidate{Index: i, Content: result.Content}
		}
		reranked, err := p.Reranker.Rerank(ctx, &domain.RerankRequest{
			ModelID: *retrievalCfg.Rerank.ModelID, OwnerID: ownerID,
			Query: query, Candidates: candidates, TopK: retrievalCfg.Rerank.TopN,
		})
		if err == nil {
			ordered := make([]domain.SearchResult, 0, len(reranked))
			seen := make(map[int]struct{}, len(reranked))
			for _, result := range reranked {
				if result.Index < 0 || result.Index >= len(results) {
					continue
				}
				if _, exists := seen[result.Index]; exists {
					continue
				}
				seen[result.Index] = struct{}{}
				ordered = append(ordered, results[result.Index])
			}
			results = ordered
		}
		// On rerank failure, retain the original ordering of valid children.
	}

	items, err := p.expandParentChunks(ctx, results, chunks)
	if err != nil {
		return nil, err
	}
	if len(items) > retrievalCfg.TopK {
		items = items[:retrievalCfg.TopK]
	}
	return items, nil
}

func (p *RetrievePipeline) currentChunks(ctx context.Context, kbIDs []string, results []domain.SearchResult) ([]domain.SearchResult, map[string]domain.ChunkRecord, error) {
	if len(results) == 0 {
		return nil, nil, nil
	}
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.ChunkID
	}
	current, err := p.DocRepo.GetChunksByIDs(ctx, ids)
	if err != nil {
		return nil, nil, errors.Wrap(errors.CodeDBError, "failed to load current chunks", err)
	}
	chunks := make(map[string]domain.ChunkRecord, len(current))
	for _, chunk := range current {
		chunks[chunk.ID] = chunk
	}
	allowed := make(map[string]struct{}, len(kbIDs))
	for _, id := range kbIDs {
		allowed[id] = struct{}{}
	}
	valid := make([]domain.SearchResult, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		chunk, exists := chunks[result.ChunkID]
		if !exists || chunk.DocID != result.DocID || chunk.KBID != result.KBID {
			continue
		}
		if _, exists := allowed[chunk.KBID]; !exists {
			continue
		}
		if _, exists := seen[chunk.ID]; exists {
			continue
		}
		if blockType, _ := chunk.Metadata["block_type"].(string); blockType == "parent" {
			continue
		}
		seen[chunk.ID] = struct{}{}
		result.Content = chunk.Content
		result.Metadata = chunk.Metadata
		valid = append(valid, result)
	}
	return valid, chunks, nil
}

func (p *RetrievePipeline) expandParentChunks(ctx context.Context, results []domain.SearchResult, chunks map[string]domain.ChunkRecord) ([]domain.RetrieveItem, error) {
	parentIDs := make([]string, 0, len(results))
	seenParents := make(map[string]struct{}, len(results))
	for _, result := range results {
		chunk := chunks[result.ChunkID]
		if chunk.ParentChunkID != nil {
			if _, exists := seenParents[*chunk.ParentChunkID]; !exists {
				seenParents[*chunk.ParentChunkID] = struct{}{}
				parentIDs = append(parentIDs, *chunk.ParentChunkID)
			}
		}
	}
	parents := make(map[string]domain.ChunkRecord, len(parentIDs))
	if len(parentIDs) > 0 {
		current, err := p.DocRepo.GetChunksByIDs(ctx, parentIDs)
		if err != nil {
			return nil, errors.Wrap(errors.CodeDBError, "failed to load parent chunks", err)
		}
		for _, parent := range current {
			parents[parent.ID] = parent
		}
	}
	items := make([]domain.RetrieveItem, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		chunk := chunks[result.ChunkID]
		content, contextID := chunk.Content, chunk.ID
		if chunk.ParentChunkID != nil {
			parent, exists := parents[*chunk.ParentChunkID]
			if !exists || parent.DocID != chunk.DocID || parent.KBID != chunk.KBID || parent.ParentChunkID != nil {
				continue
			}
			content, contextID = parent.Content, parent.ID
		}
		if _, exists := seen[contextID]; exists {
			continue
		}
		seen[contextID] = struct{}{}
		docTitle, _ := chunk.Metadata["doc_title"].(string)
		items = append(items, domain.RetrieveItem{
			ChunkID: chunk.ID, DocID: chunk.DocID, KBID: chunk.KBID,
			Content: content, MatchedContent: chunk.Content, Score: result.Score,
			DocTitle: docTitle, Metadata: chunk.Metadata,
		})
	}
	return items, nil
}
