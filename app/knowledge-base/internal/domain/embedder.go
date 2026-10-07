package domain

import "context"

type Embedder interface {
	Embed(ctx context.Context, texts []string, modelID string, ownerID *string) ([][]float32, error)
	Dimensions(modelID string) int
}
