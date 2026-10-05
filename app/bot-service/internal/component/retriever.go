package component

import "context"

// Retriever defines the adapter for knowledge base retrieval (Eino Retriever).
type Retriever interface {
	Retrieve(ctx context.Context, query string, opts *RetrieveOptions) ([]Document, error)
}

// RetrieveOptions configures a retrieval request.
type RetrieveOptions struct {
	BotID  int64
	ConvID int64
	TopK   int
	KbIDs  []int64
}

// Document is a retrieved knowledge document.
type Document struct {
	DocID    string
	Title    string
	Snippet  string
	Score    float64
	Metadata map[string]string
}

type mockRetriever struct{}

// NewMockRetriever returns a placeholder Retriever.
func NewMockRetriever() Retriever {
	return &mockRetriever{}
}

func (m *mockRetriever) Retrieve(ctx context.Context, query string, opts *RetrieveOptions) ([]Document, error) {
	return nil, nil
}
