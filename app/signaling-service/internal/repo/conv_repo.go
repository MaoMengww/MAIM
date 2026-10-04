package repo

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/message-service/pb/message"
)

// ConvRepo reads the unread read model of the message domain. Unread counts are
// owned by the message domain; the push layer only asks for the values it must
// attach to a push envelope, so it never keeps its own unread state.
type ConvRepo struct {
	msgClient message.MessageServiceClient
}

func NewConvRepo(msgClient message.MessageServiceClient) *ConvRepo {
	return &ConvRepo{msgClient: msgClient}
}

// GetUnreadCounts returns the canonical unread count of each requested user in
// the conversation as computed by the message domain.
func (r *ConvRepo) GetUnreadCounts(ctx context.Context, convID int64, userIDs []int64) (map[int64]int32, error) {
	if len(userIDs) == 0 {
		return map[int64]int32{}, nil
	}
	resp, err := r.msgClient.GetUnreadCounts(ctx, &message.GetUnreadCountsReq{
		ConversationId: convID,
		UserIds:        userIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("get unread counts: %w", err)
	}
	return resp.GetCounts(), nil
}
