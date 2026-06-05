package repo

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
)

type FriendRepo struct {
	db *database.DB
}

func NewFriendRepo(db *database.DB) *FriendRepo {
	return &FriendRepo{db: db}
}

func (r *FriendRepo) CreatePair(ctx context.Context, userID, friendID, groupID int64, genID func() int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		f1 := &model.Friend{ID: genID(), UserID: userID, FriendID: friendID, GroupID: groupID}
		f2 := &model.Friend{ID: genID(), UserID: friendID, FriendID: userID}
		if err := tx.Create(f1).Error; err != nil {
			return err
		}
		return tx.Create(f2).Error
	})
}

func (r *FriendRepo) DeletePair(ctx context.Context, userID, friendID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND friend_id = ?", userID, friendID).Delete(&model.Friend{}).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ? AND friend_id = ?", friendID, userID).Delete(&model.Friend{}).Error
	})
}

func (r *FriendRepo) GetRelation(ctx context.Context, userID, friendID int64) (*model.Friend, error) {
	var f model.Friend
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND friend_id = ?", userID, friendID).First(&f).Error
	return &f, err
}

func (r *FriendRepo) List(ctx context.Context, userID int64, groupID *int64, offset, limit int) ([]model.Friend, int64, error) {
	var friends []model.Friend
	var total int64
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if groupID != nil {
		q = q.Where("group_id = ?", *groupID)
	}
	if err := q.Model(&model.Friend{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&friends).Error
	return friends, total, err
}

func (r *FriendRepo) UpdateRemark(ctx context.Context, userID, friendID int64, remark string) error {
	return r.db.WithContext(ctx).Model(&model.Friend{}).
		Where("user_id = ? AND friend_id = ?", userID, friendID).
		Update("remark", remark).Error
}

func (r *FriendRepo) UpdateGroup(ctx context.Context, userID, friendID, groupID int64) error {
	return r.db.WithContext(ctx).Model(&model.Friend{}).
		Where("user_id = ? AND friend_id = ?", userID, friendID).
		Update("group_id", groupID).Error
}

func (r *FriendRepo) IsFriend(ctx context.Context, userID, targetID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Friend{}).
		Where("user_id = ? AND friend_id = ?", userID, targetID).
		Count(&count).Error
	return count > 0, err
}

func (r *FriendRepo) GetFriendIDs(ctx context.Context, userID int64) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.Friend{}).
		Where("user_id = ?", userID).Pluck("friend_id", &ids).Error
	return ids, err
}
