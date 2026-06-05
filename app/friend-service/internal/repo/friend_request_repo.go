package repo

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/model"
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

func (r *FriendRequestRepo) GetByID(ctx context.Context, id int64) (*model.FriendRequest, error) {
	var req model.FriendRequest
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&req).Error
	return &req, err
}

func (r *FriendRequestRepo) UpdateStatus(ctx context.Context, id int64, status int32) error {
	return r.db.WithContext(ctx).Model(&model.FriendRequest{}).
		Where("id = ?", id).Update("status", status).Error
}

func (r *FriendRequestRepo) ListPending(ctx context.Context, userID int64, offset, limit int) ([]model.FriendRequest, int64, error) {
	var reqs []model.FriendRequest
	var total int64
	db := r.db.WithContext(ctx).Where("to_user_id = ? AND status = ?", userID, model.FriendRequestStatusPending)
	if err := db.Model(&model.FriendRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&reqs).Error
	return reqs, total, err
}

func (r *FriendRequestRepo) ListSent(ctx context.Context, userID int64, offset, limit int) ([]model.FriendRequest, int64, error) {
	var reqs []model.FriendRequest
	var total int64
	db := r.db.WithContext(ctx).Where("from_user_id = ?", userID)
	if err := db.Model(&model.FriendRequest{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&reqs).Error
	return reqs, total, err
}

func (r *FriendRequestRepo) CheckPending(ctx context.Context, fromUserID, toUserID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.FriendRequest{}).
		Where("from_user_id = ? AND to_user_id = ? AND status = ?",
			fromUserID, toUserID, model.FriendRequestStatusPending).
		Count(&count).Error
	return count > 0, err
}
