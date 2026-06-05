package client

import (
	"context"

	pushpb "github.com/maomeng/aim/pkg/pb/push"
	"github.com/zeromicro/go-zero/zrpc"
)

// WsGatewayClient wraps ws-gateway gRPC client.
type WsGatewayClient struct {
	cli pushpb.InternalPushServiceClient
}

// NewWsGatewayClient creates a new ws-gateway client wrapper.
func NewWsGatewayClient(c zrpc.Client) *WsGatewayClient {
	return &WsGatewayClient{
		cli: pushpb.NewInternalPushServiceClient(c.Conn()),
	}
}

// StreamToConv sends a raw message to all online users in a conversation.
func (c *WsGatewayClient) StreamToConv(ctx context.Context, convID int64, msg []byte) error {
	_, err := c.cli.StreamToConv(ctx, &pushpb.StreamToConvReq{
		ConvId:  convID,
		Message: msg,
	})
	return err
}

// StreamToUser sends a raw message to a specific user's connections.
func (c *WsGatewayClient) StreamToUser(ctx context.Context, userID int64, msg []byte) error {
	_, err := c.cli.StreamToUser(ctx, &pushpb.StreamToUserReq{
		UserId:  userID,
		Message: msg,
	})
	return err
}
