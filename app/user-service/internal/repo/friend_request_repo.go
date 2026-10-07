package repo

import (
	"context"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"gorm.io/gorm"

	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type FriendRequestRepo struct {
	db *database.DB
}

func NewFriendRequestRepo(db *database.DB) *FriendRequestRepo {
	return &FriendRequestRepo{db: db}
}

func (r *FriendRequestRepo) Create(ctx context.Context, req *model.FriendRequest) error {
	return r.db.WithContext(ctx).Create(req).Error
}

func (r *FriendRequestRepo) GetByID(ctx context.Context, id string) (*model.FriendRequest, error) {
	var req model.FriendRequest
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&req).Error
	return &req, err
}

func (r *FriendRequestRepo) UpdateStatus(ctx context.Context, id string, status int32) error {
	result := r.db.WithContext(ctx).Model(&model.FriendRequest{}).
		Where("id = ? AND status = ?", id, model.FriendRequestStatusPending).Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New(errors.CodeConflict, "request already handled")
	}
	return nil
}

func (r *FriendRequestRepo) ListPending(ctx context.Context, userID string, offset, limit int) ([]model.FriendRequest, int64, error) {
	var reqs []model.FriendRequest
	var total int64
	db := r.db.WithContext(ctx).Where("to_user_id = ? AND status = ?", userID, model.FriendRequestStatusPending)
	if err := db.Model(&model.FriendRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&reqs).Error
	return reqs, total, err
}

func (r *FriendRequestRepo) ListSent(ctx context.Context, userID string, offset, limit int) ([]model.FriendRequest, int64, error) {
	var reqs []model.FriendRequest
	var total int64
	db := r.db.WithContext(ctx).Where("from_user_id = ?", userID)
	if err := db.Model(&model.FriendRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&reqs).Error
	return reqs, total, err
}

func (r *FriendRequestRepo) CheckPending(ctx context.Context, fromUserID, toUserID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.FriendRequest{}).
		Where("from_user_id = ? AND to_user_id = ? AND status = ?",
			fromUserID, toUserID, model.FriendRequestStatusPending).
		Count(&count).Error
	return count > 0, err
}
func (r *FriendRequestRepo) Accept(ctx context.Context, request *model.FriendRequest) error {
	id1, err := identity.New()
	if err != nil {
		return err
	}
	id2, err := identity.New()
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.FriendRequest{}).Where("id = ? AND status = ?", request.ID, model.FriendRequestStatusPending).Update("status", model.FriendRequestStatusAccepted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New(errors.CodeConflict, "request already handled")
		}
		friends := []model.Friend{{ID: id1, UserID: request.FromUserID, FriendID: request.ToUserID}, {ID: id2, UserID: request.ToUserID, FriendID: request.FromUserID}}
		return tx.Create(&friends).Error
	})
}
