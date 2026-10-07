package conversationservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	messagepb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/database"
	pkgerrors "github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func currentUser(ctx context.Context) (string, error) {
	userID, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if userID == "" {
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("user-id")
		if len(values) == 1 {
			userID = values[0]
		}
	}
	if identity.Validate(userID) != nil {
		return "", pkgerrors.ErrUnauthorized
	}
	return userID, nil
}

func validateRequest(ctx context.Context, userID string, identities ...string) error {
	if identity.Validate(userID) != nil {
		return ErrInvalidParam
	}
	for _, id := range identities {
		if identity.Validate(id) != nil {
			return ErrInvalidParam
		}
	}
	caller, err := currentUser(ctx)
	if err != nil {
		return err
	}
	if caller != userID {
		return pkgerrors.ErrForbidden
	}
	return nil
}

func memberIdentity(member *model.ConversationMember) string {
	if member.MemberType == model.MemberTypeBot && member.BotID != nil {
		return *member.BotID
	}
	if member.UserID != nil {
		return *member.UserID
	}
	return ""
}

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

// emitSystemMessage 发送系统消息到会话（同进程，走与用户消息相同的写入路径）
func emitSystemMessage(ctx context.Context, svcCtx *svc.ServiceContext, convID, operatorID string, action, detail string, relatedUserIDs []string) {
	if svcCtx.SendSystemMessage == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action":   action,
		"operator": operatorID,
	})
	go func() {
		if err := svcCtx.SendSystemMessage(context.Background(), &messagepb.SendSystemMessageReq{
			ConversationId: convID,
			ActorId:        &operatorID,
			ActorType:      "user",
			Action:         action,
			Detail:         detail,
			RelatedUserIds: relatedUserIDs,
			Payload:        string(payload),
		}); err != nil {
			svcCtx.Logger.WithContext(context.Background()).Errorf("emit system message failed: conv=%s action=%s err=%v", convID, action, err)
		}
	}()
}

// batchEmitSystemMessage 为多个相关用户各发送一条系统消息
func batchEmitSystemMessage(ctx context.Context, svcCtx *svc.ServiceContext, convID, operatorID string, action, detailTemplate string, userIDs []string) {
	for _, uid := range userIDs {
		emitSystemMessage(ctx, svcCtx, convID, uid, action, fmt.Sprintf(detailTemplate, uid), []string{uid})
	}
}

// withLockedConversation serializes aggregate writes with the message write path.
// Every repository call in fn must use r so business state and outbox commit together.
func withLockedConversation(ctx context.Context, svcCtx *svc.ServiceContext, convID string, fn func(*gorm.DB, *repo.ConversationRepo, *model.Conversation) error) error {
	if identity.Validate(convID) != nil {
		return ErrInvalidParam
	}
	return svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", convID).Take(&conv).Error; err != nil {
			return err
		}
		return fn(tx, repo.NewConversationRepo(&database.DB{DB: tx}), &conv)
	})
}

// A nil recipients slice means every current human member. Explicit recipients
// scope private settings; removals use IDs captured before deleting membership.
func publishConversationChange(ctx context.Context, svcCtx *svc.ServiceContext, tx *gorm.DB, convID string, recipients, removedRecipients []string) error {
	if recipients == nil {
		if err := tx.WithContext(ctx).Model(&model.ConversationMember{}).
			Where("conv_id = ? AND member_type = ?", convID, model.MemberTypeUser).
			Order("user_id").Pluck("user_id", &recipients).Error; err != nil {
			return err
		}
	}
	if len(recipients) > 0 {
		if err := svcCtx.PublishInboxChange(ctx, tx, map[string]any{
			"kind": "conversation.upsert", "conv_id": convID, "recipient_ids": recipients,
		}); err != nil {
			return err
		}
	}
	if len(removedRecipients) > 0 {
		return svcCtx.PublishInboxChange(ctx, tx, map[string]any{
			"kind": "conversation.removed", "conv_id": convID, "recipient_ids": removedRecipients,
		})
	}
	return nil
}
