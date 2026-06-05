package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/stretchr/testify/assert"
)

type mockVectorStore struct {
	results    []domain.SearchResult
	byIDResult map[string]domain.SearchResult
	err        error
}

func (m *mockVectorStore) Insert(ctx context.Context, docs []domain.VectorDoc) error { return m.err }
func (m *mockVectorStore) Search(ctx context.Context, vector []float32, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	return m.results, m.err
}
func (m *mockVectorStore) SparseSearch(ctx context.Context, query string, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	return m.results, m.err
}
func (m *mockVectorStore) HybridSearch(ctx context.Context, vector []float32, query string, topK int, filter domain.SearchFilter, weight domain.WeightConfig) ([]domain.SearchResult, error) {
	return m.results, m.err
}
func (m *mockVectorStore) DeleteByKB(ctx context.Context, kbID int64) error   { return m.err }
func (m *mockVectorStore) DeleteByDoc(ctx context.Context, docID int64) error { return m.err }
func (m *mockVectorStore) GetByIDs(ctx context.Context, ids []string) ([]domain.SearchResult, error) {
	if m.byIDResult != nil {
		var out []domain.SearchResult
		for _, id := range ids {
			if r, ok := m.byIDResult[id]; ok {
				out = append(out, r)
			}
		}
		return out, nil
	}
	return nil, m.err
}

type mockEmbedder struct {
	vectors [][]float32
	err     error
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	return m.vectors, m.err
}
func (m *mockEmbedder) Dimensions(model string) int { return 1536 }

type mockKBRepo struct {
	kbs      map[int64]*domain.KnowledgeBase
	bindings []domain.KnowledgeBinding
	err      error
}

func (m *mockKBRepo) Create(ctx context.Context, kb *domain.KnowledgeBase) error { return m.err }
func (m *mockKBRepo) Update(ctx context.Context, kb *domain.KnowledgeBase) error { return m.err }
func (m *mockKBRepo) Delete(ctx context.Context, kbID int64) error               { return m.err }
func (m *mockKBRepo) Get(ctx context.Context, kbID int64) (*domain.KnowledgeBase, error) {
	if kb, ok := m.kbs[kbID]; ok {
		return kb, nil
	}
	return nil, errors.New("not found")
}
func (m *mockKBRepo) ListByMode(ctx context.Context, mode string, offset, limit int) ([]domain.KnowledgeBase, error) {
	return nil, m.err
}
func (m *mockKBRepo) ListByOwner(ctx context.Context, ownerID int64, offset, limit int) ([]domain.KnowledgeBase, int64, error) {
	return nil, 0, m.err
}
func (m *mockKBRepo) Bind(ctx context.Context, binding *domain.KnowledgeBinding) error { return m.err }
func (m *mockKBRepo) Unbind(ctx context.Context, kbID int64, targetType string, targetID int64) error {
	return m.err
}
func (m *mockKBRepo) ListBindingsByTarget(ctx context.Context, targetType string, targetID int64) ([]domain.KnowledgeBinding, error) {
	return m.bindings, m.err
}
func (m *mockKBRepo) ListBindingsByKB(ctx context.Context, kbID int64) ([]domain.KnowledgeBinding, error) {
	return m.bindings, m.err
}
func (m *mockKBRepo) UpdateCounts(ctx context.Context, kbID int64) error { return m.err }

func TestExpandParentChunks_NoParentChild(t *testing.T) {
	vs := &mockVectorStore{
		results: []domain.SearchResult{
			{DocID: "c0", Content: "child 0", Score: 0.9, Metadata: map[string]any{}},
		},
	}
	p := &RetrievePipeline{
		VectorStore: vs,
		Logger:      logx.DefaultLogger(),
	}

	results := []domain.SearchResult{
		{DocID: "c0", Content: "child 0", Score: 0.9, Metadata: map[string]any{}},
	}
	expanded, err := p.expandParentChunks(context.Background(), results)
	assert.NoError(t, err)
	assert.Len(t, expanded, 1)
	assert.Equal(t, "child 0", expanded[0].Content)
}

func TestExpandParentChunks_ExpandsChildToParent(t *testing.T) {
	parentContent := "This is the parent chunk with full context."
	vs := &mockVectorStore{
		byIDResult: map[string]domain.SearchResult{
			"p0": {DocID: "p0", Content: parentContent, Score: 0.0},
		},
	}
	p := &RetrievePipeline{
		VectorStore: vs,
		Logger:      logx.DefaultLogger(),
	}

	results := []domain.SearchResult{
		{
			DocID:   "c0",
			Content: "child 0 short text",
			Score:   0.95,
			Metadata: map[string]any{
				"block_type": "child",
				"parent_id":  "p0",
			},
		},
	}

	expanded, err := p.expandParentChunks(context.Background(), results)
	assert.NoError(t, err)
	assert.Len(t, expanded, 1)
	assert.Equal(t, "child 0 short text", expanded[0].Content)
	assert.Equal(t, float32(0.95), expanded[0].Score)
}

func TestExpandParentChunks_DeduplicatesSameParent(t *testing.T) {
	parentContent := "Parent with multiple children."
	vs := &mockVectorStore{
		byIDResult: map[string]domain.SearchResult{
			"p0": {DocID: "p0", Content: parentContent, Score: 0.0},
		},
	}
	p := &RetrievePipeline{
		VectorStore: vs,
		Logger:      logx.DefaultLogger(),
	}

	results := []domain.SearchResult{
		{
			DocID:   "c0",
			Content: "child 0",
			Score:   0.95,
			Metadata: map[string]any{
				"block_type": "child",
				"parent_id":  "p0",
			},
		},
		{
			DocID:   "c1",
			Content: "child 1",
			Score:   0.85,
			Metadata: map[string]any{
				"block_type": "child",
				"parent_id":  "p0",
			},
		},
	}

	expanded, err := p.expandParentChunks(context.Background(), results)
	assert.NoError(t, err)
	assert.Len(t, expanded, 2, "should keep both children as-is")
}

func TestExpandParentChunks_MixedChildAndRegular(t *testing.T) {
	parentContent := "Parent content"
	vs := &mockVectorStore{
		byIDResult: map[string]domain.SearchResult{
			"p0": {DocID: "p0", Content: parentContent, Score: 0.0},
		},
	}
	p := &RetrievePipeline{
		VectorStore: vs,
		Logger:      logx.DefaultLogger(),
	}

	results := []domain.SearchResult{
		{
			DocID:   "c0",
			Content: "child 0",
			Score:   0.95,
			Metadata: map[string]any{
				"block_type": "child",
				"parent_id":  "p0",
			},
		},
		{
			DocID:    "r0",
			Content:  "regular chunk without parent",
			Score:    0.70,
			Metadata: map[string]any{},
		},
	}

	expanded, err := p.expandParentChunks(context.Background(), results)
	assert.NoError(t, err)
	assert.Len(t, expanded, 2, "should have parent entry + regular chunk")
}

func TestExpandParentChunks_GetByIDsFails_GracefulDegrade(t *testing.T) {
	vs := &mockVectorStore{
		err: errors.New("milvus unreachable"),
	}
	p := &RetrievePipeline{
		VectorStore: vs,
		Logger:      logx.DefaultLogger(),
	}

	results := []domain.SearchResult{
		{
			DocID:   "c0",
			Content: "child 0",
			Score:   0.9,
			Metadata: map[string]any{
				"block_type": "child",
				"parent_id":  "p0",
			},
		},
	}

	expanded, err := p.expandParentChunks(context.Background(), results)
	assert.NoError(t, err)
	assert.Len(t, expanded, 1, "should return original results on error")
	assert.Equal(t, "child 0", expanded[0].Content)
}
