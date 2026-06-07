package pipeline

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
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
	return &domain.ParentChildChunks{
		Children: []domain.Chunk{{Index: 0, Content: doc.RawText, TokenCount: 2, Metadata: map[string]any{"block_type": "child"}}},
	}, nil
}

func (m mockChunker) Strategy() string { return "mock" }

type mockDocRepo struct {
	updateStatusCalls int
	lastStatus        domain.DocStatus
}

func (m *mockDocRepo) Create(ctx context.Context, doc *domain.Document) error { return nil }
func (m *mockDocRepo) Update(ctx context.Context, doc *domain.Document) error { return nil }
func (m *mockDocRepo) Delete(ctx context.Context, docID int64) error          { return nil }
func (m *mockDocRepo) Get(ctx context.Context, docID int64) (*domain.Document, error) {
	return &domain.Document{}, nil
}
func (m *mockDocRepo) GetByHash(ctx context.Context, kbID int64, contentHash string) (*domain.Document, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *mockDocRepo) ListByKB(ctx context.Context, kbID int64, offset, limit int, status string) ([]domain.Document, int64, error) {
	return nil, 0, nil
}
func (m *mockDocRepo) UpdateStatus(ctx context.Context, docID int64, status domain.DocStatus, errMsg string) error {
	m.updateStatusCalls++
	m.lastStatus = status
	return nil
}
func (m *mockDocRepo) CreateChunks(ctx context.Context, chunks []domain.ChunkRecord) error {
	return nil
}
func (m *mockDocRepo) CreateParentChunk(ctx context.Context, chunk *domain.ChunkRecord) error {
	return nil
}
func (m *mockDocRepo) DeleteChunksByDoc(ctx context.Context, docID int64) error { return nil }
func (m *mockDocRepo) GetChunksByDocID(ctx context.Context, docID int64, offset, limit int) ([]domain.ChunkRecord, error) {
	return nil, nil
}
func (m *mockDocRepo) GetParentChunksByDocID(ctx context.Context, docID int64) ([]domain.ChunkRecord, error) {
	return nil, nil
}
func (m *mockDocRepo) UpdateStages(ctx context.Context, docID int64, stages []domain.Stage) error {
	return nil
}
func (m *mockDocRepo) CountChunksByDocID(ctx context.Context, docID int64) (int64, error) {
	return 0, nil
}

func TestIngestPipelineEmitsProgressEvents(t *testing.T) {
	doc := &domain.Document{ID: 100, KBID: 200, MinioKey: "doc.txt"}
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("new snowflake node: %v", err)
	}
	var got []event.RealtimeEvent
	pipe := &IngestPipeline{
		Parser:      mockParser{},
		Chunker:     mockChunker{},
		VectorStore: &mockVectorStore{},
		FileStore:   mockFileStore{},
		DocRepo:     &mockDocRepo{},
		KBRepo:      &mockKBRepo{},
		Snowflake:   node,
		RetryLimit:  0,
		Logger:      logx.DefaultLogger(),
		Progress: func(ctx context.Context, doc *domain.Document, evt event.RealtimeEvent) {
			got = append(got, evt)
		},
	}

	err = pipe.Run(context.Background(), doc, domain.PipelineConfig{}, 0, 0)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 progress events, got %d", len(got))
	}
	if got[0].Type != event.EventTypeKnowledgeParsing {
		t.Fatalf("expected parsing event, got %s", got[0].Type)
	}
	if got[1].Type != event.EventTypeKnowledgeChunking {
		t.Fatalf("expected chunking event, got %s", got[1].Type)
	}
	if got[2].Type != event.EventTypeKnowledgeEmbedding {
		t.Fatalf("expected embedding event, got %s", got[2].Type)
	}
	if got[3].Type != event.EventTypeKnowledgeReady {
		t.Fatalf("expected ready event, got %s", got[3].Type)
	}
	if got[3].Level != event.EventLevelSuccess {
		t.Fatalf("expected success level, got %s", got[3].Level)
	}
}

func TestRunStage_Success(t *testing.T) {
	doc := &domain.Document{ID: 100}
	pipe := &IngestPipeline{
		DocRepo:    &mockDocRepo{},
		RetryLimit: 2,
		Logger:     logx.DefaultLogger(),
	}
	called := false
	err := pipe.runStage(context.Background(), doc, domain.DocStatusParsing, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !called {
		t.Fatal("expected fn to be called")
	}
}

func TestRunStage_FailsThenSucceeds(t *testing.T) {
	doc := &domain.Document{ID: 100}
	pipe := &IngestPipeline{
		DocRepo:    &mockDocRepo{},
		RetryLimit: 3,
		Logger:     logx.DefaultLogger(),
	}
	attempts := 0
	err := pipe.runStage(context.Background(), doc, domain.DocStatusEmbedding, func() error {
		attempts++
		if attempts < 2 {
			return errors.New(errors.CodeInternal, "transient error")
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
	doc := &domain.Document{ID: 100}
	repo := &mockDocRepo{}
	pipe := &IngestPipeline{
		DocRepo:    repo,
		RetryLimit: 1,
		Logger:     logx.DefaultLogger(),
	}
	err := pipe.runStage(context.Background(), doc, domain.DocStatusChunking, func() error {
		return errors.New(errors.CodeInternal, "persistent error")
	})
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
}

func TestRunStage_UpdatesStatusOnCall(t *testing.T) {
	doc := &domain.Document{ID: 100}
	repo := &mockDocRepo{}
	pipe := &IngestPipeline{
		DocRepo:    repo,
		RetryLimit: 0,
		Logger:     logx.DefaultLogger(),
	}
	_ = pipe.runStage(context.Background(), doc, domain.DocStatusParsing, func() error {
		return errors.New(errors.CodeInternal, "fail")
	})
	if repo.updateStatusCalls < 1 {
		t.Fatal("expected at least one UpdateStatus call")
	}
	if repo.lastStatus != domain.DocStatusParsing {
		t.Fatalf("expected status parsing, got %s", repo.lastStatus)
	}
}
