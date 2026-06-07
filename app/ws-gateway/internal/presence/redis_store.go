package presence

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const presenceTTL = 120 * time.Second

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) deviceKey(userID int64) string {
	return fmt.Sprintf("user:%d:devices", userID)
}

func (s *RedisStore) deviceDetailKey(userID int64, deviceID string) string {
	return fmt.Sprintf("user:%d:device:%s", userID, deviceID)
}

func (s *RedisStore) subKey(targetUserID int64) string {
	return fmt.Sprintf("presence:sub:%d", targetUserID)
}

func (s *RedisStore) SetDevice(ctx context.Context, userID int64, deviceID string, timestamp int64) error {
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, s.deviceKey(userID), deviceID, timestamp)
	pipe.Expire(ctx, s.deviceKey(userID), presenceTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisStore) RemoveDevice(ctx context.Context, userID int64, deviceID string) error {
	return s.client.HDel(ctx, s.deviceKey(userID), deviceID).Err()
}

func (s *RedisStore) DeviceCount(ctx context.Context, userID int64) (int, error) {
	count, err := s.client.HLen(ctx, s.deviceKey(userID)).Result()
	return int(count), err
}

func (s *RedisStore) GetDevices(ctx context.Context, userID int64) ([]DeviceInfo, error) {
	result, err := s.client.HGetAll(ctx, s.deviceKey(userID)).Result()
	if err != nil {
		return nil, err
	}
	devices := make([]DeviceInfo, 0, len(result))
	for deviceID := range result {
		devices = append(devices, DeviceInfo{DeviceID: deviceID})
	}
	return devices, nil
}

func (s *RedisStore) SetDeviceStatus(ctx context.Context, userID int64, deviceID, field, value string) error {
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, s.deviceDetailKey(userID, deviceID), field, value)
	pipe.Expire(ctx, s.deviceDetailKey(userID, deviceID), presenceTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisStore) RemoveDeviceStatus(ctx context.Context, userID int64, deviceID string) error {
	return s.client.Del(ctx, s.deviceDetailKey(userID, deviceID)).Err()
}

func (s *RedisStore) Subscribe(ctx context.Context, subscriberID, targetUserID int64) error {
	return s.client.SAdd(ctx, s.subKey(targetUserID), strconv.FormatInt(subscriberID, 10)).Err()
}

func (s *RedisStore) Unsubscribe(ctx context.Context, subscriberID, targetUserID int64) error {
	return s.client.SRem(ctx, s.subKey(targetUserID), strconv.FormatInt(subscriberID, 10)).Err()
}

func (s *RedisStore) GetSubscribers(ctx context.Context, targetUserID int64) ([]int64, error) {
	members, err := s.client.SMembers(ctx, s.subKey(targetUserID)).Result()
	if err != nil {
		return nil, err
	}
	subs := make([]int64, 0, len(members))
	for _, m := range members {
		id, err := strconv.ParseInt(m, 10, 64)
		if err != nil {
			continue
		}
		subs = append(subs, id)
	}
	return subs, nil
}

func (s *RedisStore) RemoveSubscriptions(ctx context.Context, subscriberID int64) error {
	iter := s.client.Scan(ctx, 0, "presence:sub:*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		s.client.SRem(ctx, key, strconv.FormatInt(subscriberID, 10))
	}
	return iter.Err()
}

func (s *RedisStore) RefreshDeviceTTL(ctx context.Context, userID int64, deviceID string, timestamp int64) error {
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, s.deviceKey(userID), deviceID, timestamp)
	pipe.Expire(ctx, s.deviceKey(userID), presenceTTL)
	pipe.Expire(ctx, s.deviceDetailKey(userID, deviceID), presenceTTL)
	_, err := pipe.Exec(ctx)
	return err
}
