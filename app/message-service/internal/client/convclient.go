package client

import (
	"context"

	conversationpb "github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

// ConvClient is the minimal interface message-service needs from conversation-service.
type ConvClient interface {
	IsMember(ctx context.Context, conversationID, userID int64) (bool, error)
	GetMuteStatus(ctx context.Context, conversationID, userID int64) (isMuted bool, isMutedAll bool, muteUntil int64, err error)
	GetMembers(ctx context.Context, conversationID int64) ([]int64, error)
	GetConvMembers(ctx context.Context, convID int64) ([]int64, error)
	UpdateConversationLastMessage(ctx context.Context, convID, lastMsgID, maxSeq int64, preview string) error
	ListUserConvIDs(ctx context.Context, userID int64) ([]int64, error)
	PreCheckSend(ctx context.Context, conversationID, userID int64) (isMember bool, isMuted bool, isMutedAll bool, muteUntil int64, convType int32, otherMemberIDs []int64, err error)
}

type defaultConvClient struct {
	cli conversationpb.ConversationServiceClient
}

func NewConvClient(c zrpc.Client) ConvClient {
	return &defaultConvClient{
		cli: conversationpb.NewConversationServiceClient(c.Conn()),
	}
}

func (c *defaultConvClient) IsMember(ctx context.Context, conversationID, userID int64) (bool, error) {
	resp, err := c.cli.IsMember(ctx, &conversationpb.IsMemberReq{
		ConversationId: conversationID,
		UserId:         userID,
	})
	if err != nil {
		return false, err
	}
	return resp.GetIsMember(), nil
}

func (c *defaultConvClient) GetConvMembers(ctx context.Context, convID int64) ([]int64, error) {
	return c.GetMembers(ctx, convID)
}

func (c *defaultConvClient) GetMembers(ctx context.Context, conversationID int64) ([]int64, error) {
	resp, err := c.cli.GetMembers(ctx, &conversationpb.GetMembersReq{
		ConversationId: conversationID,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(resp.GetMembers()))
	for i, m := range resp.GetMembers() {
		ids[i] = m.GetUserId()
	}
	return ids, nil
}

func (c *defaultConvClient) GetMuteStatus(ctx context.Context, conversationID, userID int64) (bool, bool, int64, error) {
	resp, err := c.cli.GetMuteStatus(ctx, &conversationpb.GetMuteStatusReq{
		ConversationId: conversationID,
		UserId:         userID,
	})
	if err != nil {
		return false, false, 0, err
	}
	return resp.GetIsMuted(), resp.GetIsMutedAll(), resp.GetMuteUntil(), nil
}

func (c *defaultConvClient) UpdateConversationLastMessage(ctx context.Context, convID, lastMsgID, maxSeq int64, preview string) error {
	_, err := c.cli.UpdateConversationLastMessage(ctx, &conversationpb.UpdateConversationLastMessageReq{
		ConversationId:     convID,
		LastMessageId:      lastMsgID,
		MaxSeq:             maxSeq,
		LastMessagePreview: preview,
	})
	return err
}

func (c *defaultConvClient) PreCheckSend(ctx context.Context, conversationID, userID int64) (bool, bool, bool, int64, int32, []int64, error) {
	resp, err := c.cli.PreCheckSend(ctx, &conversationpb.PreCheckSendReq{
		ConversationId: conversationID,
		UserId:         userID,
	})
	if err != nil {
		return false, false, false, 0, 0, nil, err
	}
	return resp.GetIsMember(), resp.GetIsMuted(), resp.GetIsMutedAll(), resp.GetMuteUntil(), int32(resp.GetConvType()), resp.GetMemberIds(), nil
}

func (c *defaultConvClient) ListUserConvIDs(ctx context.Context, userID int64) ([]int64, error) {
	resp, err := c.cli.ListConversations(ctx, &conversationpb.ListConversationsReq{
		UserId: userID,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(resp.GetConversations()))
	for i, conv := range resp.GetConversations() {
		ids[i] = conv.GetId()
	}
	return ids, nil
}

// Ensure interface satisfaction.
var _ ConvClient = (*defaultConvClient)(nil)

// Ensure no unused imports.
var _ = common.BaseResponse{}
var _ = grpc.CallOption(nil)
