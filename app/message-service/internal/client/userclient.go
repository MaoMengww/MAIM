package client

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

// UserClient is the minimal interface message-service needs from user-service.
type UserClient interface {
	ListAllIDs(ctx context.Context) ([]int64, error)
	BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]string, error)
}

type defaultUserClient struct {
	cli userpb.UserServiceClient
}

func NewUserClient(c zrpc.Client) UserClient {
	return &defaultUserClient{
		cli: userpb.NewUserServiceClient(c.Conn()),
	}
}

func (c *defaultUserClient) BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	resp, err := c.cli.BatchGetUserInfo(ctx, &userpb.BatchGetUserInfoReq{UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	result := make(map[int64]string, len(resp.Users))
	for _, u := range resp.Users {
		result[u.Id] = u.Username
	}
	return result, nil
}

func (c *defaultUserClient) ListAllIDs(ctx context.Context) ([]int64, error) {
	resp, err := c.cli.ListAllUserIDs(ctx, &common.Empty{})
	if err != nil {
		return nil, err
	}
	return resp.UserIds, nil
}

// Ensure interface satisfaction.
var _ UserClient = (*defaultUserClient)(nil)

// Ensure no unused imports.
var _ = grpc.CallOption(nil)
