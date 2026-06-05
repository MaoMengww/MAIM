package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/signaling-service/internal/model"
	"gorm.io/gorm"
)

type DeviceTokenRepo struct {
	db *gorm.DB
}

func NewDeviceTokenRepo(db *gorm.DB) *DeviceTokenRepo {
	return &DeviceTokenRepo{db: db}
}

func (r *DeviceTokenRepo) Upsert(ctx context.Context, userID int64, deviceID, platform, token, provider string) error {
	dt := &model.DeviceToken{
		UserID: userID, DeviceID: deviceID, Platform: platform,
		Token: token, Provider: provider,
		CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	}
	return r.db.WithContext(ctx).
		Where("user_id = ? AND device_id = ?", userID, deviceID).
		Assign(dt).FirstOrCreate(dt).Error
}

func (r *DeviceTokenRepo) Delete(ctx context.Context, userID int64, deviceID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND device_id = ?", userID, deviceID).
		Delete(&model.DeviceToken{}).Error
}

func (r *DeviceTokenRepo) GetByUserIDs(ctx context.Context, userIDs []int64) ([]model.DeviceToken, error) {
	var tokens []model.DeviceToken
	err := r.db.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&tokens).Error
	return tokens, err
}
