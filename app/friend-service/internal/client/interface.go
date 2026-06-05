package client

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
)

type UserClientInterface interface {
	GetUserInfo(ctx context.Context, userID int64) (*userpb.UserInfo, error)
	BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]*userpb.UserInfo, error)
}

var _ UserClientInterface = (*UserClient)(nil)
