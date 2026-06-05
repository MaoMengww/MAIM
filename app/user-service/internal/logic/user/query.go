package user

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"gorm.io/gorm"
)

func (l *Logic) GetUserInfo(ctx context.Context, req *userpb.GetUserInfoReq) (*userpb.UserInfo, error) {
	u, err := l.userRepo.GetByID(ctx, req.UserId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(1004, "user not found")
		}
		return nil, errors.Wrap(1010, "get user failed", err)
	}
	return modelToUserInfo(u), nil
}

func (l *Logic) BatchGetUserInfo(ctx context.Context, req *userpb.BatchGetUserInfoReq) (*userpb.BatchGetUserInfoResp, error) {
	if len(req.UserIds) == 0 {
		return &userpb.BatchGetUserInfoResp{}, nil
	}
	users, err := l.userRepo.BatchGetByIDs(ctx, req.UserIds)
	if err != nil {
		return nil, errors.Wrap(1010, "batch get users failed", err)
	}
	infos := make([]*userpb.UserInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, modelToUserInfo(u))
	}
	return &userpb.BatchGetUserInfoResp{Users: infos}, nil
}

func (l *Logic) SearchUsers(ctx context.Context, req *userpb.SearchUsersReq) (*userpb.SearchUsersResp, error) {
	if req.Keyword == "" {
		return &userpb.SearchUsersResp{}, nil
	}
	page := int32(1)
	pageSize := int32(20)
	if req.Pagination != nil {
		if req.Pagination.Page > 0 {
			page = req.Pagination.Page
		}
		if req.Pagination.PageSize > 0 {
			pageSize = req.Pagination.PageSize
		}
	}
	offset := int(page-1) * int(pageSize)
	users, total, err := l.userRepo.Search(ctx, req.Keyword, offset, int(pageSize))
	if err != nil {
		return nil, errors.Wrap(1010, "search users failed", err)
	}
	infos := make([]*userpb.UserInfo, 0, len(users))
	for _, u := range users {
		infos = append(infos, modelToUserInfo(u))
	}
	totalPages := int32(0)
	if total > 0 {
		totalPages = int32((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return &userpb.SearchUsersResp{
		Users: infos,
		Pagination: &commonpb.PaginationResp{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}

func (l *Logic) ListAllUserIDs(ctx context.Context) (*userpb.ListAllUserIDsResp, error) {
	ids, err := l.userRepo.ListAllIDs(ctx)
	if err != nil {
		return nil, errors.Wrap(1010, "list all user ids failed", err)
	}
	return &userpb.ListAllUserIDsResp{UserIds: ids}, nil
}
