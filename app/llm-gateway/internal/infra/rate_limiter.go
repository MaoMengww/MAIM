package infra

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/core/limit"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

// RateLimiter implements domain.RateLimiter.
//
// RPM rate limiting ("Allow") uses go-zero's PeriodLimit (fixed-window counter via
// Redis INCRBY + EXPIRE) instead of a manual sorted-set sliding window.
// Concurrency limiting ("IncrConcurrency"/"DecrConcurrency") keeps the original
// Redis INCR/DECR approach because go-zero does not provide this out of the box.
type RateLimiter struct {
	rpmLimiter        *limit.PeriodLimit
	rdb               *redis.Redis
	defaultConcurrency int
}

// NewRateLimiter creates a RateLimiter backed by go-zero's Redis client.
// defaultRPM is enforced over a 60-second fixed window via PeriodLimit.
func NewRateLimiter(redisHost, redisPass string, defaultRPM, defaultConcurrency int) *RateLimiter {
	rds := redis.MustNewRedis(redis.RedisConf{
		Host: redisHost,
		Type: redis.NodeType,
		Pass: redisPass,
	})
	return &RateLimiter{
		rpmLimiter:        limit.NewPeriodLimit(60, defaultRPM, rds, "llm:rate:rpm:"),
		rdb:               rds,
		defaultConcurrency: defaultConcurrency,
	}
}

// Allow checks whether the caller is within the RPM budget for the given model.
// It delegates to go-zero's PeriodLimit.TakeCtx, which runs a Lua script that
// INCRBYs a Redis key and sets a 60-second TTL on first access.
//
// Returns CodeTooManyRequests when the quota is exceeded. On Redis errors the
// request is allowed through (fail-open) to avoid blocking all traffic.
func (rl *RateLimiter) Allow(ctx context.Context, model string) error {
	if rl.rdb == nil {
		return nil
	}
	code, err := rl.rpmLimiter.TakeCtx(ctx, model)
	if err != nil {
		// Redis error is not critical for rate limiting on its own;
		// fail-open: allow the request through to avoid blocking all traffic.
		return nil
	}
	if code == limit.OverQuota {
		return errors.New(errors.CodeTooManyRequests, fmt.Sprintf("rate limit exceeded for model %s", model))
	}
	return nil
}

// IncrConcurrency increments the concurrency counter for the given model.
// Returns CodeTooManyRequests if the concurrency limit is exceeded.
func (rl *RateLimiter) IncrConcurrency(ctx context.Context, model string) error {
	if rl.rdb == nil {
		return nil
	}
	key := fmt.Sprintf(consts.CacheKeyLLMConcurrency, model)
	result, err := rl.rdb.IncrCtx(ctx, key)
	if err != nil {
		return nil
	}
	if int(result) > rl.defaultConcurrency {
		rl.rdb.DecrCtx(ctx, key)
		return errors.New(errors.CodeTooManyRequests, fmt.Sprintf("concurrency limit exceeded for model %s", model))
	}
	return nil
}

// DecrConcurrency decrements the concurrency counter for the given model.
func (rl *RateLimiter) DecrConcurrency(ctx context.Context, model string) error {
	if rl.rdb == nil {
		return nil
	}
	key := fmt.Sprintf(consts.CacheKeyLLMConcurrency, model)
	_, err := rl.rdb.DecrCtx(ctx, key)
	return err
}
