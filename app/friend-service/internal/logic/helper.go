package logic

import (
	"github.com/maomeng/aim/app/friend-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"
)

func usernameFromUser(u *userpb.UserInfo) string {
	if u != nil {
		return u.Username
	}
	return ""
}

func avatarFromUser(u *userpb.UserInfo) string {
	if u != nil {
		return u.Avatar
	}
	return ""
}

func paginationParams(p *common.Pagination) (offset, limit int) {
	if p == nil {
		return 0, 20
	}
	page := int(p.GetPage())
	pageSize := int(p.GetPageSize())
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return (page - 1) * pageSize, pageSize
}

func paginationResp(page, pageSize int, total int64) *common.PaginationResp {
	totalPages := int32(0)
	if pageSize > 0 {
		totalPages = int32((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return &common.PaginationResp{
		Page:       int32(page),
		PageSize:   int32(pageSize),
		Total:      total,
		TotalPages: totalPages,
	}
}

func statusToString(s int32) string {
	switch s {
	case model.FriendRequestStatusPending:
		return "pending"
	case model.FriendRequestStatusAccepted:
		return "accepted"
	case model.FriendRequestStatusRejected:
		return "rejected"
	case model.FriendRequestStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func grpcError(err error) error {
	return pkg_errors.ToGRPCError(err)
}
