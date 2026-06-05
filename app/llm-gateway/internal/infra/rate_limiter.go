package infra

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"
	goredis "github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	rdb                *goredis.Client
	defaultRPM         int
	defaultConcurrency int
}

func NewRateLimiter(rdb *goredis.Client, defaultRPM, defaultConcurrency int) *RateLimiter {
	return &RateLimiter{
		rdb:                rdb,
		defaultRPM:         defaultRPM,
		defaultConcurrency: defaultConcurrency,
	}
}

func (rl *RateLimiter) Allow(ctx context.Context, model string) error {
	if rl.rdb == nil {
		return nil
	}
	key := fmt.Sprintf(consts.CacheKeyLLMRPM, model)
	now := time.Now().UnixMilli()
	windowStart := now - 60000

	pipe := rl.rdb.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(windowStart, 10))
	cardCmd := pipe.ZCard(ctx, key)
	pipe.ZAdd(ctx, key, goredis.Z{Score: float64(now), Member: fmt.Sprintf("%d", now)})
	if _, err := pipe.Exec(ctx); err != nil {
		// Redis error is not critical for rate limiting on its own;
		// fail-closed: allow the request through to avoid blocking all traffic.
		return nil
	}

	count, err := cardCmd.Result()
	if err != nil {
		return nil
	}

	if int(count) >= rl.defaultRPM {
		return errors.New(errors.CodeTooManyRequests, fmt.Sprintf("rate limit exceeded for model %s", model))
	}
	return nil
}

func (rl *RateLimiter) IncrConcurrency(ctx context.Context, model string) error {
	if rl.rdb == nil {
		return nil
	}
	key := fmt.Sprintf(consts.CacheKeyLLMConcurrency, model)
	result, err := rl.rdb.Incr(ctx, key).Result()
	if err != nil {
		return nil
	}
	if int(result) > rl.defaultConcurrency {
		rl.rdb.Decr(ctx, key)
		return errors.New(errors.CodeTooManyRequests, fmt.Sprintf("concurrency limit exceeded for model %s", model))
	}
	return nil
}

func (rl *RateLimiter) DecrConcurrency(ctx context.Context, model string) error {
	if rl.rdb == nil {
		return nil
	}
	key := fmt.Sprintf(consts.CacheKeyLLMConcurrency, model)
	return rl.rdb.Decr(ctx, key).Err()
}
