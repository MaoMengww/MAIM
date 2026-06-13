package streamcache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	keyPrefixCache = "stream:cache:"
	keyPrefixMeta  = "stream:meta:"
)

// Config holds stream cache settings.
type Config struct {
	Enabled            bool
	TTL                time.Duration
	MaxChunksPerStream int
}

// StreamCache caches streaming message chunks in Redis for reconnection replay.
type StreamCache struct {
	client *redis.Client
	cfg    Config
}

// New creates a new StreamCache.
func New(client *redis.Client, cfg Config) *StreamCache {
	return &StreamCache{client: client, cfg: cfg}
}

// Enabled returns whether the stream cache is active.
func (s *StreamCache) Enabled() bool {
	return s.cfg.Enabled
}

// chunkMsg is the minimal structure needed to parse incoming push messages.
type chunkMsg struct {
	Type     string `json:"type"`
	StreamID string `json:"stream_id"`
	Seq      int64  `json:"seq"`
}

// ShouldCache checks if a raw message is a bot streaming chunk that should be cached.
// It returns the stream_id, whether this is a "done" chunk, and whether it should be cached.
func ShouldCache(raw []byte) (streamID string, isDone bool, ok bool) {
	var m chunkMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false, false
	}
	if m.StreamID == "" {
		return "", false, false
	}
	// Only cache bot.streaming.* messages
	if len(m.Type) < 14 || m.Type[:14] != "bot.streaming." {
		return "", false, false
	}
	return m.StreamID, m.Type == "bot.streaming.done", true
}

// cacheKey returns the Redis key for a stream's chunk list.
func cacheKey(streamID string) string {
	return keyPrefixCache + streamID
}

// Store pushes a chunk into the stream's Redis list.
func (s *StreamCache) Store(ctx context.Context, streamID string, raw []byte) error {
	if !s.cfg.Enabled {
		return nil
	}

	key := cacheKey(streamID)

	// Check current length; if at capacity, stop writing
	length, err := s.client.LLen(ctx, key).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("stream cache llen: %w", err)
	}
	if length >= int64(s.cfg.MaxChunksPerStream) {
		return nil // silent drop
	}

	pipe := s.client.Pipeline()
	pipe.RPush(ctx, key, raw)
	pipe.Expire(ctx, key, s.cfg.TTL)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("stream cache store: %w", err)
	}
	return nil
}

// Done cleans up the cached chunks for a completed stream.
func (s *StreamCache) Done(ctx context.Context, streamID string) error {
	if !s.cfg.Enabled {
		return nil
	}
	pipe := s.client.Pipeline()
	pipe.Del(ctx, cacheKey(streamID))
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("stream cache done: %w", err)
	}
	return nil
}

// Replay returns all cached chunks with seq > fromSeq for a stream, in order.
// Returns nil slice if the stream is not in cache.
func (s *StreamCache) Replay(ctx context.Context, streamID string, fromSeq int64) ([][]byte, error) {
	if !s.cfg.Enabled {
		return nil, nil
	}

	key := cacheKey(streamID)
	all, err := s.client.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("stream cache replay: %w", err)
	}
	if len(all) == 0 {
		return nil, nil
	}

	var result [][]byte
	for _, rawStr := range all {
		var m chunkMsg
		if err := json.Unmarshal([]byte(rawStr), &m); err != nil {
			continue
		}
		if m.Seq > fromSeq {
			result = append(result, []byte(rawStr))
		}
	}
	return result, nil
}
