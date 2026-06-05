package domain

import "context"

type DocumentRepo interface {
	Create(ctx context.Context, doc *Document) error
	Update(ctx context.Context, doc *Document) error
	Delete(ctx context.Context, docID int64) error
	Get(ctx context.Context, docID int64) (*Document, error)
	ListByKB(ctx context.Context, kbID int64, offset, limit int, status string) ([]Document, int64, error)
	UpdateStatus(ctx context.Context, docID int64, status DocStatus, errMsg string) error
	UpdateStages(ctx context.Context, docID int64, stages []Stage) error
	CreateChunks(ctx context.Context, chunks []ChunkRecord) error
	CreateParentChunk(ctx context.Context, chunk *ChunkRecord) error
	DeleteChunksByDoc(ctx context.Context, docID int64) error
	GetChunksByDocID(ctx context.Context, docID int64, offset, limit int) ([]ChunkRecord, error)
	GetParentChunksByDocID(ctx context.Context, docID int64) ([]ChunkRecord, error)
	CountChunksByDocID(ctx context.Context, docID int64) (int64, error)
}
