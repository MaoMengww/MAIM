package connections

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maomeng/aim/pkg/identity"
	"github.com/redis/go-redis/v9"
)

type Kind string

const (
	User Kind = "user"
	Bot  Kind = "bot"
)

// Route identifies one connection incarnation, not merely a reusable device id.
type Route struct {
	Kind       Kind   `json:"kind"`
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	InstanceID string `json:"instance_id"`
	Generation string `json:"generation"`
	LastActive int64  `json:"last_active"`
}
type Store struct {
	client redis.UniversalClient
	ttl    time.Duration
}

func New(client redis.UniversalClient, ttl time.Duration) *Store {
	return &Store{client: client, ttl: ttl}
}
func DeviceKey(kind Kind, id string, device string) string {
	return fmt.Sprintf("rt:conn:{%s:%s}:device:%s", kind, id, base64.RawURLEncoding.EncodeToString([]byte(device)))
}
func (s *Store) keys(kind Kind, id string, device string) (string, string) {
	return fmt.Sprintf("rt:conn:{%s:%s}:devices", kind, id), DeviceKey(kind, id, device)
}

var registerScript = redis.NewScript(`
local now=redis.call('TIME'); local ms=now[1]*1000+math.floor(now[2]/1000)
local old=redis.call('GET',KEYS[2])
local route=cjson.decode(ARGV[1]);route.last_active=tonumber(now[1])
redis.call('SET',KEYS[2],cjson.encode(route),'PX',ARGV[2])
redis.call('ZADD',KEYS[1],ms+ARGV[2],KEYS[2])
redis.call('PEXPIRE',KEYS[1],ARGV[2])
return old or ''`)

func (s *Store) Register(ctx context.Context, r Route) (*Route, error) {
	if err := identity.Validate(r.ID); err != nil {
		return nil, err
	}
	index, key := s.keys(r.Kind, r.ID, r.DeviceID)
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	old, err := registerScript.Run(ctx, s.client, []string{index, key}, string(raw), s.ttl.Milliseconds()).Text()
	if err != nil {
		return nil, err
	}
	if old == "" {
		return nil, nil
	}
	var previous Route
	if err = json.Unmarshal([]byte(old), &previous); err != nil {
		return nil, err
	}
	return &previous, nil
}

var refreshScript = redis.NewScript(`
local current=redis.call('GET',KEYS[2]); if not current then return 0 end
local route=cjson.decode(current); if route.generation~=ARGV[1] or route.instance_id~=ARGV[2] then return 0 end
local now=redis.call('TIME');local ms=now[1]*1000+math.floor(now[2]/1000)
route.last_active=tonumber(now[1]);redis.call('SET',KEYS[2],cjson.encode(route),'PX',ARGV[3]);redis.call('ZADD',KEYS[1],ms+ARGV[3],KEYS[2]);redis.call('PEXPIRE',KEYS[1],ARGV[3]);return 1`)

func (s *Store) Refresh(ctx context.Context, r Route) (bool, error) {
	if err := identity.Validate(r.ID); err != nil {
		return false, err
	}
	index, key := s.keys(r.Kind, r.ID, r.DeviceID)
	n, err := refreshScript.Run(ctx, s.client, []string{index, key}, r.Generation, r.InstanceID, s.ttl.Milliseconds()).Int()
	return n == 1, err
}

var removeScript = redis.NewScript(`
local current=redis.call('GET',KEYS[2]);if not current then redis.call('ZREM',KEYS[1],KEYS[2]);return 0 end
local route=cjson.decode(current);if route.generation~=ARGV[1] or route.instance_id~=ARGV[2] then return 0 end
redis.call('DEL',KEYS[2]);redis.call('ZREM',KEYS[1],KEYS[2]);return 1`)

func (s *Store) Remove(ctx context.Context, r Route) (bool, error) {
	if err := identity.Validate(r.ID); err != nil {
		return false, err
	}
	index, key := s.keys(r.Kind, r.ID, r.DeviceID)
	n, err := removeScript.Run(ctx, s.client, []string{index, key}, r.Generation, r.InstanceID).Int()
	return n == 1, err
}

var listScript = redis.NewScript(`
local now=redis.call('TIME');local ms=now[1]*1000+math.floor(now[2]/1000)
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',ms)
local keys=redis.call('ZRANGE',KEYS[1],0,-1);local routes={}
for _,key in ipairs(keys) do local raw=redis.call('GET',key);if raw then table.insert(routes,raw) else redis.call('ZREM',KEYS[1],key) end end
return routes`)

func (s *Store) List(ctx context.Context, kind Kind, id string) ([]Route, error) {
	if err := identity.Validate(id); err != nil {
		return nil, err
	}
	index, _ := s.keys(kind, id, "")
	raw, err := listScript.Run(ctx, s.client, []string{index}).StringSlice()
	if err != nil {
		return nil, err
	}
	routes := make([]Route, 0, len(raw))
	for _, value := range raw {
		var r Route
		if err := json.Unmarshal([]byte(value), &r); err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, nil
}
func (s *Store) Owns(ctx context.Context, r Route) (bool, error) {
	if err := identity.Validate(r.ID); err != nil {
		return false, err
	}
	_, key := s.keys(r.Kind, r.ID, r.DeviceID)
	raw, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var current Route
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return false, err
	}
	return current.Generation == r.Generation && current.InstanceID == r.InstanceID, nil
}
func (s *Store) IsOnline(ctx context.Context, id string) bool {
	routes, err := s.List(ctx, User, id)
	return err == nil && len(routes) > 0
}
func (s *Store) Subscribe(ctx context.Context, subscriber, target string) error {
	if err := identity.Validate(subscriber); err != nil {
		return err
	}
	if err := identity.Validate(target); err != nil {
		return err
	}
	return s.client.SAdd(ctx, fmt.Sprintf("rt:presence:sub:%s", target), subscriber).Err()
}
func (s *Store) Unsubscribe(ctx context.Context, subscriber, target string) error {
	if err := identity.Validate(subscriber); err != nil {
		return err
	}
	if err := identity.Validate(target); err != nil {
		return err
	}
	return s.client.SRem(ctx, fmt.Sprintf("rt:presence:sub:%s", target), subscriber).Err()
}
func (s *Store) Subscribers(ctx context.Context, target string) ([]string, error) {
	if err := identity.Validate(target); err != nil {
		return nil, err
	}
	values, err := s.client.SMembers(ctx, fmt.Sprintf("rt:presence:sub:%s", target)).Result()
	if err != nil {
		return nil, err
	}
	for _, id := range values {
		if err := identity.Validate(id); err != nil {
			return nil, err
		}
	}
	return values, nil
}
