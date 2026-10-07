package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testKBID          = "01902ee3-8b7e-7fa1-96fd-ec908c0ace21"
	testDocID         = "01902ee3-8b7e-7fa1-96fd-ec908c0ace22"
	testParentID      = "01902ee3-8b7e-7fa1-96fd-ec908c0ace23"
	testChildID       = "01902ee3-8b7e-7fa1-96fd-ec908c0ace24"
	testSecondChildID = "01902ee3-8b7e-7fa1-96fd-ec908c0ace25"
	testRegularID     = "01902ee3-8b7e-7fa1-96fd-ec908c0ace26"
)

type mockVectorStore struct {
	results []domain.SearchResult
	docs    []domain.VectorDoc
	err     error
}

func (m *mockVectorStore) Insert(ctx context.Context, docs []domain.VectorDoc) error {
	m.docs = docs
	return m.err
}
func (m *mockVectorStore) Search(ctx context.Context, vector []float32, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	return m.results, m.err
}
func (m *mockVectorStore) SparseSearch(ctx context.Context, query string, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	return m.results, m.err
}
func (m *mockVectorStore) HybridSearch(ctx context.Context, vector []float32, query string, topK int, filter domain.SearchFilter, weight domain.WeightConfig) ([]domain.SearchResult, error) {
	return m.results, m.err
}
func (m *mockVectorStore) DeleteByKB(ctx context.Context, kbID string) error { return m.err }
func (m *mockVectorStore) DeleteByDoc(ctx context.Context, docID string) error {
	m.docs = nil
	return m.err
}
func (m *mockVectorStore) GetByIDs(ctx context.Context, ids []string) ([]domain.SearchResult, error) {
	return nil, m.err
}

type mockEmbedder struct {
	vectors [][]float32
	err     error
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string, modelID string, ownerID *string) ([][]float32, error) {
	return m.vectors, m.err
}
func (m *mockEmbedder) Dimensions(model string) int { return 1536 }

type mockKBRepo struct {
	kbs      map[string]*domain.KnowledgeBase
	bindings []domain.KnowledgeBinding
	err      error
}

func (m *mockKBRepo) Create(ctx context.Context, kb *domain.KnowledgeBase) error { return m.err }
func (m *mockKBRepo) Update(ctx context.Context, kb *domain.KnowledgeBase) error { return m.err }
func (m *mockKBRepo) Delete(ctx context.Context, kbID string) error              { return m.err }
func (m *mockKBRepo) Get(ctx context.Context, kbID string) (*domain.KnowledgeBase, error) {
	if kb, ok := m.kbs[kbID]; ok {
		return kb, nil
	}
	return nil, errors.New("not found")
}
func (m *mockKBRepo) ListByMode(ctx context.Context, mode string, offset, limit int) ([]domain.KnowledgeBase, error) {
	return nil, m.err
}
func (m *mockKBRepo) ListByOwner(ctx context.Context, ownerID string, offset, limit int) ([]domain.KnowledgeBase, int64, error) {
	return nil, 0, m.err
}
func (m *mockKBRepo) ResolveModelID(ctx context.Context, modelName string) (string, error) {
	return "01902ee3-8b7e-7fa1-96fd-ec908c0ace20", m.err
}
func (m *mockKBRepo) Bind(ctx context.Context, binding *domain.KnowledgeBinding) error { return m.err }
func (m *mockKBRepo) Unbind(ctx context.Context, kbID string, targetType string, targetID string) error {
	return m.err
}
func (m *mockKBRepo) ListBindingsByTarget(ctx context.Context, targetType string, targetID string) ([]domain.KnowledgeBinding, error) {
	return m.bindings, m.err
}
func (m *mockKBRepo) ListBindingsByKB(ctx context.Context, kbID string) ([]domain.KnowledgeBinding, error) {
	return m.bindings, m.err
}
func (m *mockKBRepo) UpdateCounts(ctx context.Context, kbID string) error { return m.err }

func retrievalFixture(chunks []domain.ChunkRecord, results []domain.SearchResult) *RetrievePipeline {
	docs := &mockDocRepo{
		docs:   map[string]*domain.Document{testDocID: {ID: testDocID, KBID: testKBID, Status: domain.DocStatusReady}},
		chunks: make(map[string]domain.ChunkRecord, len(chunks)),
	}
	for _, chunk := range chunks {
		docs.chunks[chunk.ID] = chunk
	}
	return &RetrievePipeline{
		DocRepo: docs, VectorStore: &mockVectorStore{results: results},
		Embedder: &mockEmbedder{vectors: [][]float32{{1}}}, Logger: logx.DefaultLogger(),
	}
}

func retrieveFixtureItems(t *testing.T, pipe *RetrievePipeline) []domain.RetrieveItem {
	t.Helper()
	items, err := pipe.Retrieve(t.Context(), []string{testKBID}, "query", domain.RetrievalConfig{Mode: "vector", TopK: 5}, "01902ee3-8b7e-7fa1-96fd-ec908c0ace20", nil)
	require.NoError(t, err)
	return items
}

func TestRetrieve_NoParentChild(t *testing.T) {
	pipe := retrievalFixture(
		[]domain.ChunkRecord{{ID: testChildID, DocID: testDocID, KBID: testKBID, Content: "current content"}},
		[]domain.SearchResult{{ChunkID: testChildID, DocID: testDocID, KBID: testKBID, Content: "stale vector content", Score: 0.9, Metadata: map[string]any{"doc_id": "wrong document"}}},
	)
	items := retrieveFixtureItems(t, pipe)
	require.Len(t, items, 1)
	assert.Equal(t, "current content", items[0].Content)
	assert.Equal(t, "current content", items[0].MatchedContent)
	assert.Equal(t, testChildID, items[0].ChunkID)
	assert.Equal(t, testDocID, items[0].DocID)
	assert.Equal(t, testKBID, items[0].KBID)
}

func TestRetrieve_ExpandsChildToParent(t *testing.T) {
	parentID := testParentID
	pipe := retrievalFixture(
		[]domain.ChunkRecord{
			{ID: testParentID, DocID: testDocID, KBID: testKBID, Content: "parent full context"},
			{ID: testChildID, DocID: testDocID, KBID: testKBID, ParentChunkID: &parentID, Content: "matched child"},
		},
		[]domain.SearchResult{{ChunkID: testChildID, DocID: testDocID, KBID: testKBID, Content: "matched child", Score: 0.95}},
	)
	items := retrieveFixtureItems(t, pipe)
	require.Len(t, items, 1)
	assert.Equal(t, "parent full context", items[0].Content)
	assert.Equal(t, "matched child", items[0].MatchedContent)
	assert.Equal(t, testChildID, items[0].ChunkID)
	assert.Equal(t, testDocID, items[0].DocID)
	assert.Equal(t, testKBID, items[0].KBID)
	assert.Equal(t, float32(0.95), items[0].Score)
}

func TestRetrieve_DeduplicatesSameParent(t *testing.T) {
	parentID := testParentID
	pipe := retrievalFixture(
		[]domain.ChunkRecord{
			{ID: testParentID, DocID: testDocID, KBID: testKBID, Content: "parent with multiple children"},
			{ID: testChildID, DocID: testDocID, KBID: testKBID, ParentChunkID: &parentID, Content: "stronger child"},
			{ID: testSecondChildID, DocID: testDocID, KBID: testKBID, ParentChunkID: &parentID, Content: "weaker child"},
		},
		[]domain.SearchResult{
			{ChunkID: testChildID, DocID: testDocID, KBID: testKBID, Score: 0.95},
			{ChunkID: testSecondChildID, DocID: testDocID, KBID: testKBID, Score: 0.85},
		},
	)
	items := retrieveFixtureItems(t, pipe)
	require.Len(t, items, 1)
	assert.Equal(t, "parent with multiple children", items[0].Content)
	assert.Equal(t, "stronger child", items[0].MatchedContent)
	assert.Equal(t, testChildID, items[0].ChunkID)
	assert.Equal(t, float32(0.95), items[0].Score)
}

func TestRetrieve_MixedChildAndRegular(t *testing.T) {
	parentID := testParentID
	missingParent := "01902ee3-8b7e-7fa1-96fd-ec908c0ace27"
	failedDocID := "01902ee3-8b7e-7fa1-96fd-ec908c0ace28"
	failedChildID := "01902ee3-8b7e-7fa1-96fd-ec908c0ace29"
	pipe := retrievalFixture(
		[]domain.ChunkRecord{
			{ID: testParentID, DocID: testDocID, KBID: testKBID, Content: "parent content"},
			{ID: testChildID, DocID: testDocID, KBID: testKBID, ParentChunkID: &parentID, Content: "child content"},
			{ID: testRegularID, DocID: testDocID, KBID: testKBID, Content: "regular content"},
			{ID: testSecondChildID, DocID: testDocID, KBID: testKBID, ParentChunkID: &missingParent, Content: "orphan must stay hidden"},
			{ID: failedChildID, DocID: failedDocID, KBID: testKBID, Content: "failed document must stay hidden"},
		},
		[]domain.SearchResult{
			{ChunkID: testSecondChildID, DocID: testDocID, KBID: testKBID, Score: 0.99},
			{ChunkID: failedChildID, DocID: failedDocID, KBID: testKBID, Score: 0.98},
			{ChunkID: testChildID, DocID: testDocID, KBID: testKBID, Score: 0.95},
			{ChunkID: testRegularID, DocID: testDocID, KBID: testKBID, Score: 0.70},
		},
	)
	pipe.DocRepo.(*mockDocRepo).docs[failedDocID] = &domain.Document{ID: failedDocID, KBID: testKBID, Status: domain.DocStatusFailed}
	items := retrieveFixtureItems(t, pipe)
	require.Len(t, items, 2)
	assert.Equal(t, "parent content", items[0].Content)
	assert.Equal(t, "child content", items[0].MatchedContent)
	assert.Equal(t, "regular content", items[1].Content)
}

func TestRetrieve_RelationshipReadFailsClosed(t *testing.T) {
	pipe := retrievalFixture(nil, []domain.SearchResult{{ChunkID: testChildID, DocID: testDocID, KBID: testKBID, Content: "unverified child", Score: 0.9}})
	failure := errors.New("relationship store unavailable")
	pipe.DocRepo.(*mockDocRepo).err = failure
	items, err := pipe.Retrieve(t.Context(), []string{testKBID}, "query", domain.RetrievalConfig{Mode: "vector"}, "01902ee3-8b7e-7fa1-96fd-ec908c0ace20", nil)
	assert.ErrorIs(t, err, failure)
	assert.Empty(t, items)
}
