package repo

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"

	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type BlockRepo struct {
	db *database.DB
}

func NewBlockRepo(db *database.DB) *BlockRepo {
	return &BlockRepo{db: db}
}

func (r *BlockRepo) Create(ctx context.Context, userID, blockedUserID string) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	b := &model.UserBlock{ID: id, UserID: userID, BlockedUserID: blockedUserID}
	return r.db.WithContext(ctx).Create(b).Error
}

func (r *BlockRepo) Delete(ctx context.Context, userID, blockedUserID string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND blocked_user_id = ?", userID, blockedUserID).
		Delete(&model.UserBlock{}).Error
}

func (r *BlockRepo) List(ctx context.Context, userID string, offset, limit int) ([]model.UserBlock, int64, error) {
	var blocks []model.UserBlock
	var total int64
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if err := q.Model(&model.UserBlock{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&blocks).Error
	return blocks, total, err
}

func (r *BlockRepo) IsBlocked(ctx context.Context, userID, targetID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserBlock{}).
		Where("(user_id = ? AND blocked_user_id = ?) OR (user_id = ? AND blocked_user_id = ?)",
			userID, targetID, targetID, userID).
		Count(&count).Error
	return count > 0, err
}
func (r *BlockRepo) HasBlock(ctx context.Context, userID, targetID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserBlock{}).Where("user_id = ? AND blocked_user_id = ?", userID, targetID).Count(&count).Error
	return count > 0, err
}
