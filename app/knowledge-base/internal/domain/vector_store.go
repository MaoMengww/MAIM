package domain

import "context"

type VectorDoc struct {
	DocID    string
	KBID     int64
	Vector   []float32
	Content  string
	Metadata map[string]any
}

type SearchResult struct {
	DocID    string
	Score    float32
	Content  string
	KBID     int64
	Metadata map[string]any
}

type SearchFilter struct {
	KBIDs          []int64
	ScoreThreshold float32
}

type WeightConfig struct {
	DenseWeight  float32
	SparseWeight float32
}

type VectorStore interface {
	Insert(ctx context.Context, docs []VectorDoc) error
	Search(ctx context.Context, vector []float32, topK int, filter SearchFilter) ([]SearchResult, error)
	SparseSearch(ctx context.Context, query string, topK int, filter SearchFilter) ([]SearchResult, error)
	HybridSearch(ctx context.Context, vector []float32, query string, topK int, filter SearchFilter, weight WeightConfig) ([]SearchResult, error)
	DeleteByKB(ctx context.Context, kbID int64) error
	DeleteByDoc(ctx context.Context, docID int64) error
	GetByIDs(ctx context.Context, ids []string) ([]SearchResult, error)
}
