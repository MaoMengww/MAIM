package client

import (
	"context"

	message "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/pb/common"
)

// DeliveryClient resolves current members through their owning domain before
// each streaming chunk, so removing a member stops streaming to that member.
type DeliveryClient struct {
	messages  *MessageClient
	publisher *delivery.Publisher
}

func NewDeliveryClient(messages *MessageClient, publisher *delivery.Publisher) *DeliveryClient {
	return &DeliveryClient{messages: messages, publisher: publisher}
}

func (c *DeliveryClient) PublishToConversation(ctx context.Context, convID, botID, bindingID string, raw []byte) error {
	var users []string
	for page := int32(1); ; page++ {
		resp, err := c.messages.cli.GetMembers(serviceCallContext(ctx), &message.GetMembersReq{ConversationId: convID, BotId: &botID, BindingId: &bindingID, Pagination: &common.Pagination{Page: page, PageSize: 100}})
		if err != nil {
			return err
		}
		for _, member := range resp.Members {
			users = append(users, member.GetUserId())
		}
		if len(resp.Members) < 100 {
			break
		}
	}
	if len(users) == 0 {
		return nil
	}
	return c.publisher.Publish(ctx, convID, delivery.Intent{UserIDs: users, Payload: raw})
}
