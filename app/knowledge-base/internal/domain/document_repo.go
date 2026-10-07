package domain

import "context"

type DocumentRepo interface {
	Create(ctx context.Context, doc *Document) error
	Update(ctx context.Context, doc *Document) error
	Delete(ctx context.Context, docID string) error
	Get(ctx context.Context, docID string) (*Document, error)
	GetByHash(ctx context.Context, kbID string, contentHash string) (*Document, error)
	ListByKB(ctx context.Context, kbID string, offset, limit int, status string) ([]Document, int64, error)
	UpdateStatus(ctx context.Context, docID string, status DocStatus, errMsg string) error
	UpdateStages(ctx context.Context, docID string, stages []Stage) error
	CreateChunks(ctx context.Context, chunks []ChunkRecord) error
	CreateParentChunk(ctx context.Context, chunk *ChunkRecord) error
	DeleteChunksByDoc(ctx context.Context, docID string) error
	GetChunksByDocID(ctx context.Context, docID string, offset, limit int) ([]ChunkRecord, error)
	GetParentChunksByDocID(ctx context.Context, docID string) ([]ChunkRecord, error)
	CountChunksByDocID(ctx context.Context, docID string) (int64, error)
	// GetChunksByIDs returns only chunks whose document is currently ready.
	GetChunksByIDs(ctx context.Context, ids []string) ([]ChunkRecord, error)
	// WithDocumentLock serializes a document lifecycle without keeping a transaction open.
	WithDocumentLock(ctx context.Context, docID string, fn func(context.Context) error) error
}
