package presence

import (
	"context"
	"time"

	"github.com/maomeng/aim/pkg/consts"
)

type Status string

const (
	StatusOnline  Status = consts.PresenceOnline
	StatusOffline Status = consts.PresenceOffline
)

type Store interface {
	SetDevice(ctx context.Context, userID int64, deviceID string, timestamp int64) error
	RemoveDevice(ctx context.Context, userID int64, deviceID string) error
	DeviceCount(ctx context.Context, userID int64) (int, error)
	GetDevices(ctx context.Context, userID int64) ([]DeviceInfo, error)
	SetDeviceStatus(ctx context.Context, userID int64, deviceID, field, value string) error
	RemoveDeviceStatus(ctx context.Context, userID int64, deviceID string) error
	Subscribe(ctx context.Context, subscriberID, targetUserID int64) error
	Unsubscribe(ctx context.Context, subscriberID, targetUserID int64) error
	GetSubscribers(ctx context.Context, targetUserID int64) ([]int64, error)
	RemoveSubscriptions(ctx context.Context, subscriberID int64) error
	RefreshDeviceTTL(ctx context.Context, userID int64, deviceID string, timestamp int64) error
}

type DeviceInfo struct {
	DeviceID string `json:"device_id"`
	Platform string `json:"platform"`
}

type PresenceEvent struct {
	UserID int64  `json:"user_id"`
	Status Status `json:"status"`
}

type Pusher interface {
	PushPresence(ctx context.Context, event PresenceEvent) error
}

type Manager struct {
	store  Store
	pusher Pusher
}

func NewManager(store Store, pusher Pusher) *Manager {
	return &Manager{store: store, pusher: pusher}
}

func (m *Manager) OnConnect(ctx context.Context, userID int64, deviceID, platform string) error {
	now := time.Now().Unix()
	if err := m.store.SetDevice(ctx, userID, deviceID, now); err != nil {
		return err
	}
	if err := m.store.SetDeviceStatus(ctx, userID, deviceID, "status", string(StatusOnline)); err != nil {
		return err
	}
	if err := m.store.SetDeviceStatus(ctx, userID, deviceID, "platform", platform); err != nil {
		return err
	}

	subs, err := m.store.GetSubscribers(ctx, userID)
	if err != nil || len(subs) == 0 {
		return err
	}

	if m.pusher != nil {
		return m.pusher.PushPresence(ctx, PresenceEvent{
			UserID: userID,
			Status: StatusOnline,
		})
	}
	return nil
}

func (m *Manager) OnDisconnect(ctx context.Context, userID int64, deviceID string) error {
	if err := m.store.RemoveDevice(ctx, userID, deviceID); err != nil {
		return err
	}
	if err := m.store.RemoveDeviceStatus(ctx, userID, deviceID); err != nil {
		return err
	}

	count, err := m.store.DeviceCount(ctx, userID)
	if err != nil || count > 0 {
		return err
	}

	// User fully offline
	_ = m.store.RemoveSubscriptions(ctx, userID)

	subs, err := m.store.GetSubscribers(ctx, userID)
	if err != nil || len(subs) == 0 {
		return err
	}

	if m.pusher != nil {
		return m.pusher.PushPresence(ctx, PresenceEvent{
			UserID: userID,
			Status: StatusOffline,
		})
	}
	return nil
}

func (m *Manager) Subscribe(ctx context.Context, subscriberID, targetUserID int64) error {
	return m.store.Subscribe(ctx, subscriberID, targetUserID)
}

func (m *Manager) Unsubscribe(ctx context.Context, subscriberID, targetUserID int64) error {
	return m.store.Unsubscribe(ctx, subscriberID, targetUserID)
}

func (m *Manager) RefreshTTL(ctx context.Context, userID int64, deviceID string) error {
	return m.store.RefreshDeviceTTL(ctx, userID, deviceID, time.Now().Unix())
}

func (m *Manager) IsOnline(ctx context.Context, userID int64) bool {
	count, err := m.store.DeviceCount(ctx, userID)
	return err == nil && count > 0
}
