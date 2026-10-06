package streamcache

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	Enabled            bool
	TTL                time.Duration
	MaxChunksPerStream int
}
type StreamCache struct {
	client *redis.Client
	cfg    Config
}

func New(client *redis.Client, cfg Config) *StreamCache {
	return &StreamCache{client: client, cfg: cfg}
}
func (s *StreamCache) Enabled() bool { return s.cfg.Enabled }

type chunkMsg struct {
	Type     string `json:"type"`
	StreamID string `json:"stream_id"`
	Seq      int64  `json:"seq"`
}

func (chunk *chunkMsg) UnmarshalJSON(raw []byte) error {
	type payload chunkMsg
	var next chunkMsg
	decoded := struct {
		*payload
		Seq json.RawMessage `json:"seq"`
	}{payload: (*payload)(&next)}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	value, err := sequence.ParseJSON(decoded.Seq)
	if err != nil {
		return err
	}
	next.Seq = value
	*chunk = next
	return nil
}

func ShouldCache(raw []byte) (string, bool, bool) {
	var m chunkMsg
	if json.Unmarshal(raw, &m) != nil || sequence.Validate(m.Seq) != nil || m.StreamID == "" || !strings.HasPrefix(m.Type, "bot.streaming.") {
		return "", false, false
	}
	return m.StreamID, m.Type == "bot.streaming.done", true
}

// Recipient-scoped keys are the replay authorization boundary. A stream id alone
// never grants access to another user's chunks.
func cacheKey(userID string, streamID string) string {
	return fmt.Sprintf("rt:stream:{user:%s}:%s", userID, base64.RawURLEncoding.EncodeToString([]byte(streamID)))
}

var storeScript = redis.NewScript(`
if redis.call('LLEN',KEYS[1]) < tonumber(ARGV[3]) then redis.call('RPUSH',KEYS[1],ARGV[1]) end
redis.call('PEXPIRE',KEYS[1],ARGV[2]);return 1`)

func (s *StreamCache) Store(ctx context.Context, userID string, streamID string, raw []byte, done bool) error {
	if err := identity.Validate(userID); err != nil {
		return err
	}
	var chunk chunkMsg
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return err
	}
	if err := sequence.Validate(chunk.Seq); err != nil {
		return err
	}
	if !s.cfg.Enabled {
		return nil
	}
	// Completed streams retain the same bounded TTL so reconnecting clients can
	// replay the final chunk; completion does not leak a global replay capability.
	return storeScript.Run(ctx, s.client, []string{cacheKey(userID, streamID)}, string(raw), s.cfg.TTL.Milliseconds(), s.cfg.MaxChunksPerStream).Err()
}
func (s *StreamCache) Replay(ctx context.Context, userID string, streamID string, fromSeq int64) ([][]byte, error) {
	if err := identity.Validate(userID); err != nil {
		return nil, err
	}
	// -1 is an internal sentinel for an omitted replay position, never a wire value.
	if fromSeq != -1 {
		if err := sequence.Validate(fromSeq); err != nil {
			return nil, err
		}
	}
	if !s.cfg.Enabled {
		return nil, nil
	}
	all, err := s.client.LRange(ctx, cacheKey(userID, streamID), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	var chunks [][]byte
	for _, raw := range all {
		var m chunkMsg
		if json.Unmarshal([]byte(raw), &m) == nil && sequence.Validate(m.Seq) == nil && m.Seq > fromSeq {
			chunks = append(chunks, []byte(raw))
		}
	}
	return chunks, nil
}
