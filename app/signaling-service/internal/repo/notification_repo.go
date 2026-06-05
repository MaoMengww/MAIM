package repo

import (
	"context"

	"github.com/maomeng/aim/app/signaling-service/internal/model"
	"gorm.io/gorm"
)

type NotificationRepo struct {
	db *gorm.DB
}

func NewNotificationRepo(db *gorm.DB) *NotificationRepo {
	return &NotificationRepo{db: db}
}

func (r *NotificationRepo) List(ctx context.Context, userID int64, notifType *int32, isRead *bool, page, pageSize int) ([]model.Notification, int64, error) {
	var total int64
	q := r.db.WithContext(ctx).Model(&model.Notification{}).Where("user_id = ?", userID)
	if notifType != nil {
		q = q.Where("type = ?", *notifType)
	}
	if isRead != nil {
		q = q.Where("is_read = ?", *isRead)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var notifs []model.Notification
	if err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&notifs).Error; err != nil {
		return nil, 0, err
	}
	return notifs, total, nil
}

func (r *NotificationRepo) UnreadCount(ctx context.Context, userID int64) (int64, error) {
	var count int64
	return count, r.db.WithContext(ctx).Model(&model.Notification{}).Where("user_id = ? AND is_read = ?", userID, false).Count(&count).Error
}

func (r *NotificationRepo) MarkRead(ctx context.Context, userID, notifID int64) error {
	return r.db.WithContext(ctx).Model(&model.Notification{}).Where("user_id = ? AND id = ?", userID, notifID).Update("is_read", true).Error
}

func (r *NotificationRepo) MarkAllRead(ctx context.Context, userID int64) error {
	return r.db.WithContext(ctx).Model(&model.Notification{}).Where("user_id = ?", userID).Update("is_read", true).Error
}

func (r *NotificationRepo) Delete(ctx context.Context, userID, notifID int64) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND id = ?", userID, notifID).Delete(&model.Notification{}).Error
}

func (r *NotificationRepo) Create(ctx context.Context, notif *model.Notification) error {
	return r.db.WithContext(ctx).Create(notif).Error
}
