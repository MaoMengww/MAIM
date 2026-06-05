package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/zeromicro/go-zero/core/stores/redis"
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
	return r.db.WithContext(ctx).Where("user_id = ? AND device_id = ?", d.UserID, d.DeviceID).
		Assign(d).FirstOrCreate(d).Error
}

func (r *AuthRepo) GetUserDevices(ctx context.Context, userID int64) ([]*model.UserDevice, error) {
	var devices []*model.UserDevice
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("last_active_at DESC").Find(&devices).Error
	return devices, err
}

func (r *AuthRepo) DeleteDevice(ctx context.Context, userID int64, deviceID string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND device_id = ?", userID, deviceID).Delete(&model.UserDevice{}).Error
}

func (r *AuthRepo) GetDevice(ctx context.Context, userID int64, deviceID string) (*model.UserDevice, error) {
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
// by probing the presence key written by ws-gateway.
func (r *AuthRepo) IsDeviceOnline(ctx context.Context, userID int64, deviceID string) (bool, error) {
	key := fmt.Sprintf("user:%d:device:%s", userID, deviceID)
	return r.redis.ExistsCtx(ctx, key)
}
