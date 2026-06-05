package domain

import "context"

type Embedder interface {
	Embed(ctx context.Context, texts []string, modelID int64, ownerID int64) ([][]float32, error)
	Dimensions(modelID int64) int
}
