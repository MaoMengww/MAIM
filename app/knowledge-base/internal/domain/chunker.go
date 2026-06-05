package domain

import (
	"context"
	"fmt"
)

type Chunk struct {
	Index      int
	Content    string
	TokenCount int
	KBID       int64
	DocID      int64
	StartPos   int
	EndPos     int
	Metadata   map[string]any
}

func (c *Chunk) ID() string {
	return fmt.Sprintf("kb_%d_doc_%d_chk_%d", c.KBID, c.DocID, c.Index)
}

type ParentChildChunks struct {
	Parents  []Chunk
	Children []Chunk
}

type Chunker interface {
	Chunk(ctx context.Context, doc *ParsedDocument, cfg ChunkingConfig) (*ParentChildChunks, error)
}
