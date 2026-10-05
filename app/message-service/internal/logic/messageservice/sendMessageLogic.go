package messageservicelogic

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SendMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendMessageLogic {
	return &SendMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendMessageLogic) SendMessage(in *message.SendMessageReq) (*message.SendMessageResp, error) {
	ctx := l.ctx

	// 1. 幂等校验
	if in.ClientMsgId != "" {
		key := fmt.Sprintf("messaging:idempotent:%s", in.ClientMsgId)
		ok, err := l.svcCtx.Redis.SetNX(ctx, key, "1", consts.MsgIdempotentTTL).Result()
		if err != nil {
			l.Errorf("idempotent check failed: %v", err)
			return nil, ErrIdempotentCheckFailed
		} else if !ok {
			return nil, ErrDuplicateMessage
		}
	}

	// 2. 验证调用方身份：从 gRPC metadata 提取 user-id 并与请求中的 from_user_id 比对
	callerID := callerUserID(ctx)
	if callerID == 0 {
		return nil, ErrUserIDMissing
	}
	if callerID != in.FromUserId {
		return nil, ErrSendAsOtherUser
	}

	// 3. 构造消息；展示资料可以在事务外预取，授权状态不能。
	msgID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate msg id failed", err)
	}
	now := time.Now()
	contentJSON := extractSendContent(in)

	msg := &model.Message{
		ID:           msgID,
		ConvID:       in.ConversationId,
		SenderID:     in.FromUserId,
		ClientMsgID:  in.ClientMsgId,
		MsgType:      int32(in.Type),
		Content:      contentJSON,
		ReplyToMsgID: in.GetReplyToId(),
		Status:       model.MessageStatusNormal,
		EditHistory:  model.JSONArray{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 4. 单事务：锁会话 + 权限校验 + seq + 消息 + outbox + 最新消息
	senderName := resolveReplySenderName(ctx, l.svcCtx, msg.SenderID, "user")

	var seq int64
	err = l.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		perm, err := l.svcCtx.ConversationRepo.CheckSendPermission(ctx, tx, in.ConversationId, in.FromUserId)
		if err != nil {
			return err
		}
		if !perm.IsMember {
			return ErrNotMember
		}
		if perm.IsMutedAll || (perm.IsMuted && (perm.MuteUntil == 0 || time.Now().Unix() < perm.MuteUntil)) {
			return ErrSenderMuted
		}
		// 群聊拉黑不阻止发消息；私聊直接使用同一事务读取 user 域。
		if perm.ConversationType == model.ConvTypePrivate && len(perm.OtherMemberIDs) > 0 {
			var blocks []struct {
				UserID int64
			}
			if err := tx.Table(`"user".user_blocks`).Select("user_id").
				Clauses(clause.Locking{Strength: "SHARE"}).
				Where("(user_id = ? AND blocked_user_id IN ?) OR (user_id IN ? AND blocked_user_id = ?)",
					in.FromUserId, perm.OtherMemberIDs, perm.OtherMemberIDs, in.FromUserId).
				Limit(1).Find(&blocks).Error; err != nil {
				return ErrBlockCheckFailed
			}
			if len(blocks) > 0 {
				return ErrBlockedByMember
			}
		}

		if err := persistMessage(ctx, l.svcCtx, tx, msg, senderName); err != nil {
			return err
		}
		seq = msg.Seq
		return nil
	})
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New(errors.CodeNotFound, "conversation not found")
		}
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "insert message failed", err)
	}

	metrics.MessagesSentTotal.Inc(strconv.FormatInt(int64(in.Type), 10))

	l.Infof("message sent: msg_id=%d conv_id=%d sender=%d", msgID, in.ConversationId, in.FromUserId)

	return &message.SendMessageResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}

// extractTextPreview 从消息中提取纯文本预览（只存原始文本，由前端格式化展示）
func extractTextPreview(msgType int32, content model.JSONContent) string {
	if content == nil {
		return ""
	}
	switch msgType {
	case 1: // text
		if text, ok := content["text"].(string); ok && text != "" {
			runes := []rune(text)
			if len(runes) > 20 {
				return string(runes[:20]) + "..."
			}
			return text
		}
		return "[消息]"
	default:
		return "[消息]"
	}
}

func extractSendContent(req *message.SendMessageReq) model.JSONContent {
	c := req.GetContent()
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case *message.SendMessageReq_Text:
		return model.TextContent{
			Text:           v.Text.GetText(),
			MentionUserIDs: v.Text.GetMentionUserIds(),
			MentionAll:     v.Text.GetMentionAll(),
		}.ToJSONContent()
	case *message.SendMessageReq_Image:
		return model.ImageContent{
			FileID:       v.Image.GetFileId(),
			URL:          v.Image.GetUrl(),
			ThumbnailURL: v.Image.GetThumbnailUrl(),
			Width:        v.Image.GetWidth(),
			Height:       v.Image.GetHeight(),
			Size:         v.Image.GetSize(),
			Format:       v.Image.GetFormat(),
		}.ToJSONContent()
	case *message.SendMessageReq_File:
		return model.FileContent{
			FileID:   v.File.GetFileId(),
			URL:      v.File.GetUrl(),
			Name:     v.File.GetName(),
			Size:     v.File.GetSize(),
			Ext:      v.File.GetExt(),
			MimeType: v.File.GetMimeType(),
		}.ToJSONContent()
	case *message.SendMessageReq_Video:
		return model.VideoContent{
			FileID:       v.Video.GetFileId(),
			URL:          v.Video.GetUrl(),
			ThumbnailURL: v.Video.GetThumbnailUrl(),
			Duration:     v.Video.GetDuration(),
			Width:        v.Video.GetWidth(),
			Height:       v.Video.GetHeight(),
			Size:         v.Video.GetSize(),
		}.ToJSONContent()
	case *message.SendMessageReq_Audio:
		return model.AudioContent{
			FileID:   v.Audio.GetFileId(),
			URL:      v.Audio.GetUrl(),
			Duration: v.Audio.GetDuration(),
			Size:     v.Audio.GetSize(),
		}.ToJSONContent()
	case *message.SendMessageReq_Location:
		return model.LocationContent{
			Latitude:  v.Location.GetLatitude(),
			Longitude: v.Location.GetLongitude(),
			Address:   v.Location.GetAddress(),
			Name:      v.Location.GetName(),
		}.ToJSONContent()
	case *message.SendMessageReq_Custom:
		return model.CustomContent{
			Type: v.Custom.GetType(),
			Data: v.Custom.GetData(),
		}.ToJSONContent()
	}
	return nil
}

// buildMessageCreatedPayload builds the Kafka event payload for a new message.
func buildMessageCreatedPayload(msg *model.Message, senderName string) map[string]any {
	return map[string]any{
		"message_id":      msg.ID,
		"conv_id":         msg.ConvID,
		"sender_id":       msg.SenderID,
		"sender_type":     msg.SenderType,
		"msg_type":        int64(msg.MsgType),
		"content":         msg.Content,
		"seq":             msg.Seq,
		"reply_to_msg_id": msg.ReplyToMsgID,
		"created_at":      msg.CreatedAt.Unix(),
		"sender_name":     senderName,
		"preview_text":    extractTextPreview(msg.MsgType, msg.Content),
	}
}

// persistMessage commits the content, publication intent and conversation tail
// together. The caller holds the conversation lock through transaction commit.
func persistMessage(ctx context.Context, svcCtx *svc.ServiceContext, tx *gorm.DB, msg *model.Message, senderName string) error {
	seq, err := svcCtx.SequenceRepo.NextSeq(ctx, tx, msg.ConvID)
	if err != nil {
		return err
	}
	msg.Seq = seq
	if err := tx.Create(msg).Error; err != nil {
		return err
	}
	outboxID, err := svcCtx.Snowflake.Generate()
	if err != nil {
		return err
	}
	outboxEvent := &model.OutboxEvent{
		ID: outboxID, Topic: consts.KafkaTopicMessageCreated,
		Key: fmt.Sprintf("%d", msg.ID), MaxRetries: model.DefaultMaxRetries,
	}
	if err := outboxEvent.SetPayload(buildMessageCreatedPayload(msg, senderName)); err != nil {
		return err
	}
	if err := svcCtx.OutboxRepo.Insert(ctx, tx, outboxEvent); err != nil {
		return err
	}
	return svcCtx.ConversationRepo.TouchLastMessage(ctx, tx, msg.ConvID, msg.ID, seq,
		extractTextPreview(msg.MsgType, msg.Content))
}
