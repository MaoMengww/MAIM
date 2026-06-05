package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const unreadTTL = 7 * 24 * time.Hour

type UnreadCache struct {
	rdb *redis.Client
}

func NewRedisUnreadCache(rdb *redis.Client) *UnreadCache {
	return &UnreadCache{rdb: rdb}
}

func unreadKey(userID, convID int64) string {
	return fmt.Sprintf("unread:%d:%d", userID, convID)
}

func (c *UnreadCache) GetUnreadCount(ctx context.Context, userID, convID int64) (int32, error) {
	val, err := c.rdb.Get(ctx, unreadKey(userID, convID)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int32(val), nil
}

func (c *UnreadCache) BatchGetUnreadCounts(ctx context.Context, userIDs []int64, convID int64) (map[int64]int32, error) {
	pipe := c.rdb.Pipeline()
	for _, uid := range userIDs {
		pipe.Get(ctx, unreadKey(uid, convID))
	}
	cmds, err := pipe.Exec(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]int32, len(userIDs))
	for i, uid := range userIDs {
		if i < len(cmds) {
			val, e := cmds[i].(*redis.StringCmd).Int64()
			if e == nil {
				result[uid] = int32(val)
			}
		}
	}
	return result, nil
}

func (c *UnreadCache) IncrUnreadCount(ctx context.Context, userID, convID int64) (int32, error) {
	key := unreadKey(userID, convID)
	val, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	c.rdb.Expire(ctx, key, unreadTTL)
	return int32(val), nil
}

func (c *UnreadCache) ClearUnreadCount(ctx context.Context, userID, convID int64) error {
	return c.rdb.Del(ctx, unreadKey(userID, convID)).Err()
}
