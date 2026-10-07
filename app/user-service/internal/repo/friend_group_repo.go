package repo

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"

	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
)

type FriendGroupRepo struct {
	db *database.DB
}

func NewFriendGroupRepo(db *database.DB) *FriendGroupRepo {
	return &FriendGroupRepo{db: db}
}

func (r *FriendGroupRepo) Create(ctx context.Context, userID string, name string) (string, error) {
	id, err := identity.New()
	if err != nil {
		return "", err
	}
	g := &model.FriendGroup{ID: id, UserID: userID, Name: name}
	if err := r.db.WithContext(ctx).Create(g).Error; err != nil {
		return "", err
	}
	return g.ID, nil
}

func (r *FriendGroupRepo) Update(ctx context.Context, id string, name string) error {
	return r.db.WithContext(ctx).Model(&model.FriendGroup{}).
		Where("id = ?", id).Update("name", name).Error
}

func (r *FriendGroupRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Friend{}).Where("group_id = ?", id).Update("group_id", nil).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&model.FriendGroup{}).Error
	})
}

func (r *FriendGroupRepo) List(ctx context.Context, userID string) ([]model.FriendGroup, error) {
	var groups []model.FriendGroup
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("sort_order ASC, id ASC").
		Find(&groups).Error
	return groups, err
}

func (r *FriendGroupRepo) GetByID(ctx context.Context, id string) (*model.FriendGroup, error) {
	var g model.FriendGroup
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&g).Error
	return &g, err
}
