package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/pkg/connections"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"gorm.io/gorm/clause"
)

const (
	revokedTokenKeyFmt = "revoked_token:%s"
)

type AuthRepo struct {
	db    *database.DB
	redis *redis.Redis
}

func NewAuthRepo(db *database.DB, redis *redis.Redis) *AuthRepo {
	return &AuthRepo{db: db, redis: redis}
}

// ========== Device Management ==========

func (r *AuthRepo) SaveDevice(ctx context.Context, d *model.UserDevice) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	d.ID = id
	d.LastActiveAt = time.Now()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "device_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"platform", "last_active_at"}),
	}).Create(d).Error
}

func (r *AuthRepo) GetUserDevices(ctx context.Context, userID string) ([]*model.UserDevice, error) {
	var devices []*model.UserDevice
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("last_active_at DESC").Find(&devices).Error
	return devices, err
}

func (r *AuthRepo) GetDevice(ctx context.Context, userID string, deviceID string) (*model.UserDevice, error) {
	var d model.UserDevice
	err := r.db.WithContext(ctx).Where("user_id = ? AND device_id = ?", userID, deviceID).First(&d).Error
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ========== Token Revocation (Redis) ==========

func (r *AuthRepo) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	key := fmt.Sprintf(revokedTokenKeyFmt, jti)
	if err := r.redis.SetexCtx(ctx, key, "1", int(ttl.Seconds())); err != nil {
		return err
	}
	return nil
}

func (r *AuthRepo) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	key := fmt.Sprintf(revokedTokenKeyFmt, jti)
	return r.redis.ExistsCtx(ctx, key)
}

// IsDeviceOnline checks if the device has an active WebSocket connection
// by probing the authoritative per-device connection registry entry.
func (r *AuthRepo) IsDeviceOnline(ctx context.Context, userID string, deviceID string) (bool, error) {
	key := connections.DeviceKey(connections.User, userID, deviceID)
	return r.redis.ExistsCtx(ctx, key)
}
func (r *AuthRepo) DeleteSession(ctx context.Context, userID, sessionID string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, sessionID).Delete(&model.UserDevice{}).Error
}
