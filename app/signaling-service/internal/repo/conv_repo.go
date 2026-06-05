package repo

import (
	"context"
	"fmt"

	convpb "github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/app/signaling-service/internal/model"
)

type ConvRepo struct {
	convClient convpb.ConversationServiceClient
}

func NewConvRepo(convClient convpb.ConversationServiceClient) *ConvRepo {
	return &ConvRepo{convClient: convClient}
}

func (r *ConvRepo) GetConversation(ctx context.Context, convID int64) (*model.ConvInfo, error) {
	resp, err := r.convClient.GetConversation(ctx, &convpb.GetConversationReq{ConversationId: convID})
	if err != nil {
		return nil, fmt.Errorf("get conversation via gRPC: %w", err)
	}
	return &model.ConvInfo{ID: resp.Conversation.Id, MaxSeq: resp.Conversation.MaxSeq}, nil
}

func (r *ConvRepo) BatchGetReadSeqs(ctx context.Context, userIDs []int64, convID int64) (map[int64]*model.ReadSeq, error) {
	resp, err := r.convClient.GetReadStatus(ctx, &convpb.GetReadStatusReq{ConversationId: convID})
	if err != nil {
		return nil, fmt.Errorf("get read status via gRPC: %w", err)
	}
	result := make(map[int64]*model.ReadSeq, len(resp.ReadUsers))
	for _, u := range resp.ReadUsers {
		for _, uid := range userIDs {
			if u.UserId == uid {
				result[uid] = &model.ReadSeq{UserID: u.UserId, ConvID: convID, LastReadSeq: u.LastReadSeq}
				break
			}
		}
	}
	return result, nil
}
