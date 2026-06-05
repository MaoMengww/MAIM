package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RateLimit(rdb *redis.Client, requestsPerSec, messagePerSec int) gin.HandlerFunc {
	return func(c *gin.Context) {
		var limit int

		switch {
		case c.FullPath() == "/api/v1/messages/send":
			limit = messagePerSec
		default:
			limit = requestsPerSec
		}

		key := rateLimitKey(c)
		if key == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		now := time.Now().Unix()
		windowStart := now - 1

		pipe := rdb.Pipeline()
		pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))
		pipe.ZCard(ctx, key)
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: fmt.Sprintf("%d", now)})
		pipe.Expire(ctx, key, 2*time.Second)

		cmds, err := pipe.Exec(ctx)
		if err != nil {
			c.Next()
			return
		}

		count, _ := cmds[1].(*redis.IntCmd).Result()
		if int(count) > limit {
			c.AbortWithStatusJSON(429, gin.H{
				"code":    429,
				"message": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}

func rateLimitKey(c *gin.Context) string {
	userID, exists := c.Get(CtxKeyUserID)
	if !exists {
		return ""
	}
	return fmt.Sprintf("rate:%d:%s", userID.(int64), c.FullPath())
}
