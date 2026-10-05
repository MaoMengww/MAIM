package client

import (
	"context"
	"fmt"

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

func (c *DeliveryClient) PublishToConversation(ctx context.Context, convID, botID int64, raw []byte) error {
	var users []int64
	botMember := false
	for page := int32(1); ; page++ {
		resp, err := c.messages.cli.GetMembers(ctx, &message.GetMembersReq{ConversationId: convID, UserId: botID, Pagination: &common.Pagination{Page: page, PageSize: 100}})
		if err != nil {
			return err
		}
		for _, member := range resp.Members {
			if member.MemberType == message.MemberType_MEMBER_TYPE_BOT {
				if member.BotId == botID {
					botMember = true
				}
			} else {
				users = append(users, member.UserId)
			}
		}
		if len(resp.Members) < 100 {
			break
		}
	}
	if !botMember {
		return fmt.Errorf("bot %d is not a member of conversation %d", botID, convID)
	}
	if len(users) == 0 {
		return nil
	}
	return c.publisher.Publish(ctx, convID, delivery.Intent{UserIDs: users, Payload: raw})
}
