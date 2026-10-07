package pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"gorm.io/gorm"
)

type mockFileStore struct{}

func (m mockFileStore) Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	return nil
}
func (m mockFileStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("hello world")), nil
}
func (m mockFileStore) Delete(ctx context.Context, key string) error { return nil }

type mockParser struct{}

func (m mockParser) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	return &domain.ParsedDocument{RawText: string(raw)}, nil
}
func (m mockParser) Name() string { return "mock" }

type mockChunker struct{}

func (m mockChunker) Chunk(ctx context.Context, doc *domain.ParsedDocument, cfg domain.ChunkingConfig) (*domain.ParentChildChunks, error) {
	return &domain.ParentChildChunks{Children: []domain.Chunk{{Index: 0, Content: doc.RawText, TokenCount: 2}}}, nil
}
func (m mockChunker) Strategy() string { return "mock" }

type mockDocRepo struct {
	docs   map[string]*domain.Document
	chunks map[string]domain.ChunkRecord
	err    error
}

func (m *mockDocRepo) Create(ctx context.Context, doc *domain.Document) error {
	return m.Update(ctx, doc)
}
func (m *mockDocRepo) Update(ctx context.Context, doc *domain.Document) error {
	if m.docs == nil {
		m.docs = make(map[string]*domain.Document)
	}
	copy := *doc
	m.docs[doc.ID] = &copy
	return m.err
}
func (m *mockDocRepo) Delete(ctx context.Context, docID string) error {
	delete(m.docs, docID)
	return m.err
}
func (m *mockDocRepo) Get(ctx context.Context, docID string) (*domain.Document, error) {
	if doc, exists := m.docs[docID]; exists {
		copy := *doc
		return &copy, m.err
	}
	return nil, gorm.ErrRecordNotFound
}
func (m *mockDocRepo) GetByHash(ctx context.Context, kbID string, contentHash string) (*domain.Document, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *mockDocRepo) ListByKB(ctx context.Context, kbID string, offset, limit int, status string) ([]domain.Document, int64, error) {
	return nil, 0, m.err
}
func (m *mockDocRepo) UpdateStatus(ctx context.Context, docID string, status domain.DocStatus, errMsg string) error {
	if doc := m.docs[docID]; doc != nil {
		doc.Status, doc.ErrorMessage = status, errMsg
	}
	return m.err
}
func (m *mockDocRepo) CreateChunks(ctx context.Context, chunks []domain.ChunkRecord) error {
	if m.chunks == nil {
		m.chunks = make(map[string]domain.ChunkRecord)
	}
	for _, chunk := range chunks {
		m.chunks[chunk.ID] = chunk
	}
	return m.err
}
func (m *mockDocRepo) CreateParentChunk(ctx context.Context, chunk *domain.ChunkRecord) error {
	return m.CreateChunks(ctx, []domain.ChunkRecord{*chunk})
}
func (m *mockDocRepo) DeleteChunksByDoc(ctx context.Context, docID string) error {
	for id, chunk := range m.chunks {
		if chunk.DocID == docID {
			delete(m.chunks, id)
		}
	}
	return m.err
}
func (m *mockDocRepo) GetChunksByDocID(ctx context.Context, docID string, offset, limit int) ([]domain.ChunkRecord, error) {
	return nil, m.err
}
func (m *mockDocRepo) GetParentChunksByDocID(ctx context.Context, docID string) ([]domain.ChunkRecord, error) {
	return nil, m.err
}
func (m *mockDocRepo) GetChunksByIDs(ctx context.Context, ids []string) ([]domain.ChunkRecord, error) {
	var chunks []domain.ChunkRecord
	for _, id := range ids {
		chunk, exists := m.chunks[id]
		if doc := m.docs[chunk.DocID]; exists && doc != nil && doc.Status == domain.DocStatusReady {
			chunks = append(chunks, chunk)
		}
	}
	return chunks, m.err
}
func (m *mockDocRepo) WithDocumentLock(ctx context.Context, docID string, fn func(context.Context) error) error {
	return fn(ctx)
}
func (m *mockDocRepo) UpdateStages(ctx context.Context, docID string, stages []domain.Stage) error {
	if doc := m.docs[docID]; doc != nil {
		doc.Stages = stages
	}
	return m.err
}
func (m *mockDocRepo) CountChunksByDocID(ctx context.Context, docID string) (int64, error) {
	return 0, m.err
}

func TestIngestPipelineEmitsProgressEvents(t *testing.T) {
	doc := &domain.Document{ID: testDocID, KBID: testKBID, MinioKey: "doc.txt", Status: domain.DocStatusPending}
	modelID := "01902ee3-8b7e-7fa1-96fd-ec908c0ace20"
	var got []event.RealtimeEvent
	pipe := &IngestPipeline{
		Parser: mockParser{}, Chunker: mockChunker{},
		VectorStore: &mockVectorStore{}, FileStore: mockFileStore{},
		Embedder: &mockEmbedder{vectors: [][]float32{{1}}},
		DocRepo:  &mockDocRepo{docs: map[string]*domain.Document{doc.ID: doc}},
		KBRepo:   &mockKBRepo{kbs: map[string]*domain.KnowledgeBase{testKBID: {ID: testKBID, Status: "active", OwnerType: "platform", EmbeddingModelID: &modelID}}},
		Logger:   logx.DefaultLogger(),
		Progress: func(ctx context.Context, doc *domain.Document, evt event.RealtimeEvent) { got = append(got, evt) },
	}
	if err := pipe.Run(t.Context(), doc); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []event.EventType{event.EventTypeKnowledgeParsing, event.EventTypeKnowledgeChunking, event.EventTypeKnowledgeEmbedding, event.EventTypeKnowledgeReady}
	if len(got) != len(want) {
		t.Fatalf("expected %d progress events, got %d", len(want), len(got))
	}
	for i, eventType := range want {
		if got[i].Type != eventType {
			t.Fatalf("event %d: expected %s, got %s", i, eventType, got[i].Type)
		}
	}
	if got[len(got)-1].Level != event.EventLevelSuccess {
		t.Fatalf("expected success level, got %s", got[len(got)-1].Level)
	}
}

func TestRunStage_FailsThenSucceeds(t *testing.T) {
	doc := &domain.Document{ID: testDocID}
	pipe := &IngestPipeline{DocRepo: &mockDocRepo{}, RetryLimit: 3, Logger: logx.DefaultLogger()}
	attempts := 0
	err := pipe.runStage(t.Context(), doc, domain.DocStatusEmbedding, func() error {
		attempts++
		if attempts < 2 {
			return errors.New("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestRunStage_AllRetriesFail(t *testing.T) {
	doc := &domain.Document{ID: testDocID}
	pipe := &IngestPipeline{DocRepo: &mockDocRepo{}, RetryLimit: 1, Logger: logx.DefaultLogger()}
	failure := errors.New("persistent error")
	attempts := 0
	err := pipe.runStage(t.Context(), doc, domain.DocStatusChunking, func() error {
		attempts++
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("expected final stage error to preserve its cause, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected initial attempt plus one retry, got %d", attempts)
	}
}
