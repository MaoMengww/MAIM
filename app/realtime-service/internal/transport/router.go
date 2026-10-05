package transport

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maomeng/aim/app/realtime-service/internal/offline"
	"github.com/maomeng/aim/app/realtime-service/internal/session"
	"github.com/maomeng/aim/app/realtime-service/internal/streamcache"
	registry "github.com/maomeng/aim/pkg/connections"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/redis/go-redis/v9"
)

type packet struct {
	Route  registry.Route  `json:"route"`
	Intent delivery.Intent `json:"intent"`
	Close  bool            `json:"close,omitzero"`
}
type Router struct {
	Redis        *redis.Client
	Registry     *registry.Store
	Sessions     *session.Manager
	Cache        *streamcache.StreamCache
	Offline      *offline.Service
	Logger       logx.Logger
	InstanceID   string
	WriteTimeout time.Duration
	Connections  sync.WaitGroup
	admission    sync.Mutex
	ready        atomic.Bool
	stopping     atomic.Bool
	subscription *redis.PubSub
}

func (r *Router) Ready() bool    { return r.ready.Load() && !r.stopping.Load() }
func (r *Router) StopAccepting() { r.admission.Lock(); r.stopping.Store(true); r.admission.Unlock() }
func (r *Router) BeginConnection() bool {
	r.admission.Lock()
	defer r.admission.Unlock()
	if !r.Ready() {
		return false
	}
	r.Connections.Add(1)
	return true
}
func channel(instance string) string { return "rt:node:" + instance }

// Subscribe waits for the server's SUBSCRIBE acknowledgement before registration.
func (r *Router) Subscribe(ctx context.Context) error {
	r.subscription = r.Redis.Subscribe(ctx, channel(r.InstanceID))
	if _, err := r.subscription.Receive(ctx); err != nil {
		_ = r.subscription.Close()
		return err
	}
	r.ready.Store(true)
	go func() {
		defer r.ready.Store(false)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-r.subscription.Channel():
				if !ok {
					return
				}
				var p packet
				if err := json.Unmarshal([]byte(msg.Payload), &p); err != nil {
					r.Logger.Errorf("node packet decode: %v", err)
					continue
				}
				if p.Close {
					if s := r.Sessions.Get(p.Route); s != nil {
						s.Close()
					}
					continue
				}
				if err := r.receive(ctx, p); err != nil {
					r.Logger.Errorf("node delivery: kind=%s id=%d device=%s err=%v", p.Route.Kind, p.Route.ID, p.Route.DeviceID, err)
				}
			}
		}
	}()
	return nil
}
func (r *Router) Close() error {
	r.StopAccepting()
	if r.subscription != nil {
		return r.subscription.Close()
	}
	return nil
}
func (r *Router) Replace(ctx context.Context, old *registry.Route) {
	if old == nil {
		return
	}
	raw, err := json.Marshal(packet{Route: *old, Close: true})
	if err == nil {
		err = r.Redis.Publish(ctx, channel(old.InstanceID), raw).Err()
	}
	if err != nil {
		r.Logger.Errorf("replace connection: %v", err)
	}
}
func (r *Router) OfflinePush(ctx context.Context, id int64, n *delivery.Notification) {
	if n == nil {
		return
	}
	r.Logger.WithContext(ctx).Infof("realtime.offline_fallback: kind=user id=%d node=%s", id, r.InstanceID)
	if r.Offline != nil {
		r.Offline.PushToOfflineUsers(ctx, []int64{id}, n.Title, n.Body, n.Data)
	}
}
func (r *Router) fail(ctx context.Context, p packet, cause error) error {
	r.Logger.WithContext(ctx).Errorf("realtime.delivery_failed: kind=%s id=%d device=%s node=%s generation=%s reason=%v", p.Route.Kind, p.Route.ID, p.Route.DeviceID, p.Route.InstanceID, p.Route.Generation, cause)
	_, err := r.Registry.Remove(ctx, p.Route)
	if err != nil {
		r.Logger.Errorf("remove failed route: %v", err)
	}
	if p.Route.Kind == registry.User {
		r.OfflinePush(ctx, p.Route.ID, p.Intent.Notification)
	}
	return errors.Join(cause, err)
}
func (r *Router) receive(ctx context.Context, p packet) error {
	owns, err := r.Registry.Owns(ctx, p.Route)
	if err != nil {
		return r.fail(ctx, p, err)
	}
	if !owns {
		routes, err := r.Registry.List(ctx, p.Route.Kind, p.Route.ID)
		if err != nil {
			return r.fail(ctx, p, err)
		}
		for _, route := range routes {
			if route.DeviceID == p.Route.DeviceID {
				return nil
			}
		} // a superseded generation is not a failed current route
		return r.fail(ctx, p, errors.New("connection registration expired"))
	}
	s := r.Sessions.Get(p.Route)
	if s == nil {
		return r.fail(ctx, p, errors.New("connection absent"))
	}
	if err := s.WriteMessage(p.Intent.Payload); err != nil {
		s.Close()
		return r.fail(ctx, p, err)
	}
	return nil
}

// Deliver has one path for local and remote devices: registry -> node pub/sub.
func (r *Router) Deliver(ctx context.Context, intent delivery.Intent) error {
	var failures []error
	send := func(kind registry.Kind, id int64) {
		routes, err := r.Registry.List(ctx, kind, id)
		if err != nil {
			failures = append(failures, err)
			if kind == registry.User {
				r.OfflinePush(ctx, id, intent.Notification)
			}
			return
		}
		if kind == registry.User && r.Cache != nil {
			if streamID, done, ok := streamcache.ShouldCache(intent.Payload); ok {
				if err := r.Cache.Store(ctx, id, streamID, intent.Payload, done); err != nil {
					r.Logger.Errorf("stream cache: user=%d err=%v", id, err)
				}
			}
		}
		if len(routes) == 0 {
			if kind == registry.User {
				r.OfflinePush(ctx, id, intent.Notification)
			}
			return
		}
		for _, route := range routes {
			if kind == registry.User && id == intent.ExcludeUserID && route.DeviceID == intent.ExcludeDeviceID {
				continue
			}
			p := packet{Route: route, Intent: intent}
			raw, err := json.Marshal(p)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			subscribers, err := r.Redis.Publish(ctx, channel(route.InstanceID), raw).Result()
			if err != nil || subscribers == 0 {
				if err == nil {
					err = errors.New("node has no subscriber")
				}
				failures = append(failures, r.fail(ctx, p, err))
			}
		}
	}
	users := make(map[int64]struct{}, len(intent.UserIDs))
	for _, id := range intent.UserIDs {
		if _, ok := users[id]; !ok {
			users[id] = struct{}{}
			send(registry.User, id)
		}
	}
	bots := make(map[int64]struct{}, len(intent.BotIDs))
	for _, id := range intent.BotIDs {
		if _, ok := bots[id]; !ok {
			bots[id] = struct{}{}
			send(registry.Bot, id)
		}
	}
	return errors.Join(failures...)
}
func (r *Router) Presence(ctx context.Context, id int64) error {
	ids, err := r.Registry.Subscribers(ctx, id)
	if err != nil {
		return err
	}
	status := "offline"
	if r.Registry.IsOnline(ctx, id) {
		status = "online"
	}
	payload, err := json.Marshal(map[string]any{"type": "presence", "user_id": strconv.FormatInt(id, 10), "status": status})
	if err != nil {
		return err
	}
	return r.Deliver(ctx, delivery.Intent{UserIDs: ids, Payload: payload})
}
func (r *Router) Disconnect(s *session.Session) {
	r.Sessions.Remove(s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	removed, err := r.Registry.Remove(ctx, s.Route)
	if err != nil {
		r.Logger.Errorf("disconnect registry: %v", err)
	}
	if removed && s.Route.Kind == registry.User {
		if err := r.Presence(ctx, s.Route.ID); err != nil {
			r.Logger.Errorf("disconnect presence: %v", err)
		}
	}
}
func (r *Router) Drain(ctx context.Context, duration time.Duration) {
	sessions := r.Sessions.Snapshot()
	if len(sessions) == 0 {
		return
	}
	step := duration / time.Duration(len(sessions))
	start := time.Now()
	for i, s := range sessions {
		timer := time.NewTimer(time.Until(start.Add(time.Duration(i+1) * step)))
		select {
		case <-ctx.Done():
			timer.Stop()
			for _, remaining := range sessions[i:] {
				remaining.Close()
			}
			return
		case <-timer.C:
		}
		if err := s.Restart(r.WriteTimeout); err != nil {
			r.Logger.Errorf("realtime.drain_close_failed: kind=%s id=%d device=%s reason=%v", s.Route.Kind, s.Route.ID, s.Route.DeviceID, err)
		}
	}
	r.Logger.Infof("connection drain finished: count=%d", len(sessions))
}
func (r *Router) WaitConnections(ctx context.Context) {
	done := make(chan struct{})
	go func() { r.Connections.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		r.Logger.Errorf("realtime.connection_cleanup_timeout: %v", ctx.Err())
	}
}
