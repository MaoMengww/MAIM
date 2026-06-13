package repo

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type UnreadCache struct {
	rdb *redis.Client
}

func NewUnreadCache(rdb *redis.Client) *UnreadCache {
	return &UnreadCache{rdb: rdb}
}

// GetUnreadCount 获取用户在某个会话的未读数
func (c *UnreadCache) GetUnreadCount(ctx context.Context, userID, convID int64) (int32, error) {
	key := fmt.Sprintf("unread:%d:%d", userID, convID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count, _ := strconv.ParseInt(val, 10, 32)
	return int32(count), nil
}

// SetUnreadCount 设置未读数（永不过期）
func (c *UnreadCache) SetUnreadCount(ctx context.Context, userID, convID int64, count int32) error {
	key := fmt.Sprintf("unread:%d:%d", userID, convID)
	return c.rdb.Set(ctx, key, count, 0).Err()
}

// IncrUnreadCount 增加未读数（新消息到达）
func (c *UnreadCache) IncrUnreadCount(ctx context.Context, userID, convID int64) (int32, error) {
	key := fmt.Sprintf("unread:%d:%d", userID, convID)
	pipe := c.rdb.Pipeline()

	// 增加会话未读数（键永不过期）
	incrCmd := pipe.Incr(ctx, key)

	// 增加总未读数
	totalKey := fmt.Sprintf("unread:total:%d", userID)
	pipe.Incr(ctx, totalKey)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}

	return int32(incrCmd.Val()), nil
}

// ClearUnreadCount 清零未读数（标记已读）
func (c *UnreadCache) ClearUnreadCount(ctx context.Context, userID, convID int64) error {
	key := fmt.Sprintf("unread:%d:%d", userID, convID)

	// 获取当前未读数
	oldCount, _ := c.GetUnreadCount(ctx, userID, convID)
	if oldCount == 0 {
		return nil
	}

	pipe := c.rdb.Pipeline()

	// 清零会话未读数
	pipe.Del(ctx, key)

	// 减少总未读数
	totalKey := fmt.Sprintf("unread:total:%d", userID)
	pipe.DecrBy(ctx, totalKey, int64(oldCount))

	_, err := pipe.Exec(ctx)
	return err
}

// BatchGetUnreadCounts 批量获取未读数
func (c *UnreadCache) BatchGetUnreadCounts(ctx context.Context, userIDs []int64, convID int64) (map[int64]int32, error) {
	if len(userIDs) == 0 {
		return make(map[int64]int32), nil
	}

	keys := make([]string, len(userIDs))
	for i, userID := range userIDs {
		keys[i] = fmt.Sprintf("unread:%d:%d", userID, convID)
	}

	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	result := make(map[int64]int32, len(userIDs))
	for i, val := range vals {
		if val == nil {
			result[userIDs[i]] = 0
			continue
		}
		if str, ok := val.(string); ok {
			count, _ := strconv.ParseInt(str, 10, 32)
			result[userIDs[i]] = int32(count)
		}
	}

	return result, nil
}

// GetTotalUnreadCount 获取用户的总未读数
func (c *UnreadCache) GetTotalUnreadCount(ctx context.Context, userID int64) (int32, error) {
	key := fmt.Sprintf("unread:total:%d", userID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count, _ := strconv.ParseInt(val, 10, 32)
	return int32(count), nil
}
