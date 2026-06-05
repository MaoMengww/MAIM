package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const dedupTTL = 7 * 24 * time.Hour

// Dedup checks and sets an idempotency key in Redis.
type Dedup struct {
	client *redis.Client
}

// NewDedup creates a new Dedup.
func NewDedup(client *redis.Client) *Dedup {
	return &Dedup{client: client}
}

// IsDuplicate returns true if the event has already been processed.
// Uses Redis SETNX for atomicity.
func (d *Dedup) IsDuplicate(ctx context.Context, eventID string) (bool, error) {
	if d.client == nil {
		return false, nil // skip dedup if Redis is not available
	}
	key := fmt.Sprintf("dedup:%s", eventID)
	ok, err := d.client.SetNX(ctx, key, "1", dedupTTL).Result()
	if err != nil {
		return false, err
	}
	return !ok, nil
}
