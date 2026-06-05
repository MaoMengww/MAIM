package component

import "context"

// Embedding defines the adapter for text vectorization.
type Embedding interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type mockEmbedding struct{}

// NewMockEmbedding returns a placeholder Embedding.
func NewMockEmbedding() Embedding {
	return &mockEmbedding{}
}

func (m *mockEmbedding) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range texts {
		vecs[i] = make([]float32, 1536)
	}
	return vecs, nil
}
