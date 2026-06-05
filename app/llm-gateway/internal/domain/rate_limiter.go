package domain

import "context"

type RateLimiter interface {
	Allow(ctx context.Context, model string) error
	IncrConcurrency(ctx context.Context, model string) error
	DecrConcurrency(ctx context.Context, model string) error
}
