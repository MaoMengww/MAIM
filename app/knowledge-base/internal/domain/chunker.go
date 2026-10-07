package domain

import "context"

type Chunk struct {
	ID         string
	Index      int
	Content    string
	TokenCount int
	KBID       string
	DocID      string
	StartPos   int
	EndPos     int
	Metadata   map[string]any
}

type ParentChildChunks struct {
	Parents  []Chunk
	Children []Chunk
}

type Chunker interface {
	Chunk(ctx context.Context, doc *ParsedDocument, cfg ChunkingConfig) (*ParentChildChunks, error)
}
