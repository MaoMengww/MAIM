package client

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/zrpc"
)

type FriendClient interface {
	IsBlocked(ctx context.Context, userID, targetUserID int64) (bool, error)
	IsBlockedAny(ctx context.Context, userID int64, targetUserIDs []int64) (bool, error)
}

type defaultFriendClient struct {
	cli userpb.UserServiceClient
}

func NewFriendClient(c zrpc.Client) FriendClient {
	return &defaultFriendClient{
		cli: userpb.NewUserServiceClient(c.Conn()),
	}
}

var _ FriendClient = (*defaultFriendClient)(nil)

func (c *defaultFriendClient) IsBlocked(ctx context.Context, userID, targetUserID int64) (bool, error) {
	resp, err := c.cli.IsBlocked(ctx, &userpb.IsBlockedReq{
		UserId:       userID,
		TargetUserId: targetUserID,
	})
	if err != nil {
		return false, err
	}
	return resp.GetIsBlocked(), nil
}

func (c *defaultFriendClient) IsBlockedAny(ctx context.Context, userID int64, targetUserIDs []int64) (bool, error) {
	for _, targetID := range targetUserIDs {
		blocked, err := c.IsBlocked(ctx, userID, targetID)
		if err != nil {
			return false, err
		}
		if blocked {
			return true, nil
		}
	}
	return false, nil
}
