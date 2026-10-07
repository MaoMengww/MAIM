package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/realtime-service/internal/model"
	"github.com/maomeng/aim/pkg/identity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DeviceTokenRepo struct {
	db *gorm.DB
}

func NewDeviceTokenRepo(db *gorm.DB) *DeviceTokenRepo {
	return &DeviceTokenRepo{db: db}
}

func (r *DeviceTokenRepo) Upsert(ctx context.Context, userID string, deviceID, platform, token, provider string) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	dt := &model.DeviceToken{
		ID: id, UserID: userID, DeviceID: deviceID, Platform: platform,
		Token: token, Provider: provider, CreatedAt: now, UpdatedAt: now,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "device_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"platform", "token", "provider", "updated_at"}),
	}).Create(dt).Error
}

func (r *DeviceTokenRepo) Delete(ctx context.Context, userID string, deviceID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND device_id = ?", userID, deviceID).
		Delete(&model.DeviceToken{}).Error
}

func (r *DeviceTokenRepo) GetByUserIDs(ctx context.Context, userIDs []string) ([]model.DeviceToken, error) {
	var tokens []model.DeviceToken
	err := r.db.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&tokens).Error
	return tokens, err
}
