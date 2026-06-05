package consumer

import (
	"context"
	"encoding/json"

	pushpb "github.com/maomeng/aim/pkg/pb/push"
	"github.com/zeromicro/go-zero/zrpc"
)

type GRPCPusher struct {
	client pushpb.InternalPushServiceClient
}

func NewGRPCPusher(cli zrpc.Client) *GRPCPusher {
	return &GRPCPusher{client: pushpb.NewInternalPushServiceClient(cli.Conn())}
}

func (p *GRPCPusher) PushToUsers(ctx context.Context, userIDs []int64, message json.RawMessage) error {
	_, err := p.client.PushToUsers(ctx, &pushpb.PushToUsersReq{UserIds: userIDs, Message: message})
	return err
}

func (p *GRPCPusher) PushToBot(ctx context.Context, botID int64, message json.RawMessage) error {
	_, err := p.client.PushToBot(ctx, &pushpb.PushToBotReq{BotId: botID, Message: message})
	return err
}
