package client

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/zeromicro/go-zero/zrpc"
)

type UserClient struct {
	cli zrpc.Client
}

func NewUserClient(cli zrpc.Client) *UserClient {
	return &UserClient{cli: cli}
}

func (c *UserClient) GetUserInfo(ctx context.Context, userID int64) (*userpb.UserInfo, error) {
	client := userpb.NewUserServiceClient(c.cli.Conn())
	return client.GetUserInfo(ctx, &userpb.GetUserInfoReq{UserId: userID})
}

func (c *UserClient) BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]*userpb.UserInfo, error) {
	client := userpb.NewUserServiceClient(c.cli.Conn())
	resp, err := client.BatchGetUserInfo(ctx, &userpb.BatchGetUserInfoReq{UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	result := make(map[int64]*userpb.UserInfo, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		result[u.GetId()] = u
	}
	return result, nil
}
