package conversationservice

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
)

func toProtoConv(c *model.Conversation, lastReadSeq int64, unread int32, muted, pinned bool) *conversation.Conversation {
	return &conversation.Conversation{
		Id:                 c.ID,
		Type:               conversation.ConversationType(c.Type),
		Name:               c.Name,
		Avatar:             c.Avatar,
		OwnerId:            c.OwnerID,
		MemberCount:        c.MemberCount,
		MaxSeq:             c.MaxSeq,
		LastMessageId:      c.LastMessageID,
		LastMessagePreview: c.LastMessagePreview,
		LastReadSeq:        lastReadSeq,
		UnreadCount:        unread,
		IsMuted:            muted,
		IsPinned:           pinned,
		IsMutedAll:         c.IsMutedAll,
		Announcement:       c.Announcement,
		Background:         c.Background,
		CreatedAt:          c.CreatedAt.Unix(),
		UpdatedAt:          c.UpdatedAt.Unix(),
	}
}

func emitSystemMessage(ctx context.Context, svcCtx *svc.ServiceContext, convID, operatorID int64, action, content string, metadata any) {
	if svcCtx.BotEventProducer != nil {
		payload, _ := json.Marshal(map[string]any{
			"conv_id":     convID,
			"operator_id": operatorID,
			"action":      action,
			"content":     content,
			"metadata":    metadata,
			"type":        "system",
		})
		if err := svcCtx.BotEventProducer.Send(ctx, "", payload); err != nil {
			svcCtx.Logger.WithContext(ctx).Errorf("emit system message failed: %v", err)
		}
	}
}
