package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type BroadcastRepo struct {
	db *database.DB
}

func NewBroadcastRepo(db *database.DB) *BroadcastRepo {
	return &BroadcastRepo{db: db}
}

func (r *BroadcastRepo) Insert(ctx context.Context, broadcast *model.Broadcast) error {
	return r.db.WithContext(ctx).Create(broadcast).Error
}

func (r *BroadcastRepo) GetByID(ctx context.Context, id int64) (*model.Broadcast, error) {
	var b model.Broadcast
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *BroadcastRepo) List(ctx context.Context, scope string, page, pageSize int) ([]model.Broadcast, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Broadcast{})
	if scope != "" {
		q = q.Where("scope = ?", scope)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var broadcasts []model.Broadcast
	offset := (page - 1) * pageSize
	err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&broadcasts).Error
	if err != nil {
		return nil, 0, err
	}
	return broadcasts, total, nil
}

func (r *BroadcastRepo) ListByUser(ctx context.Context, userID int64, page, pageSize int) ([]model.Broadcast, int64, error) {
	// broadcasts visible to user: scope='all' OR map in user_inbox
	q := r.db.WithContext(ctx).Model(&model.Broadcast{}).
		Where("scope = ? OR (scope = ? AND scope_target_id = ?)", "all", "user", userID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var broadcasts []model.Broadcast
	offset := (page - 1) * pageSize
	err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&broadcasts).Error
	if err != nil {
		return nil, 0, err
	}
	return broadcasts, total, nil
}
