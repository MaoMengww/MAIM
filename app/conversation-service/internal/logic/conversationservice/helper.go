package conversationservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	messagepb "github.com/maomeng/aim/app/message-service/pb/message"
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

// emitSystemMessage 发送系统消息到会话（通过 message-service gRPC）
func emitSystemMessage(ctx context.Context, svcCtx *svc.ServiceContext, convID, operatorID int64, action, detail string, relatedUserIDs []int64) {
	if svcCtx.MessageRpc == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action":   action,
		"operator": operatorID,
	})
	go func() {
		if _, err := svcCtx.MessageRpc.SendSystemMessage(context.Background(), &messagepb.SendSystemMessageReq{
			ConversationId: convID,
			ActorId:        operatorID,
			ActorType:      "user",
			Action:         action,
			Detail:         detail,
			RelatedUserIds: relatedUserIDs,
			Payload:        string(payload),
		}); err != nil {
			svcCtx.Logger.WithContext(context.Background()).Errorf("emit system message failed: conv=%d action=%s err=%v", convID, action, err)
		}
	}()
}

// batchEmitSystemMessage 为多个相关用户各发送一条系统消息
func batchEmitSystemMessage(ctx context.Context, svcCtx *svc.ServiceContext, convID, operatorID int64, action, detailTemplate string, userIDs []int64) {
	for _, uid := range userIDs {
		emitSystemMessage(ctx, svcCtx, convID, uid, action, fmt.Sprintf(detailTemplate, uid), []int64{uid})
	}
}
