package messageservicelogic

import (
	"context"
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
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
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
		key := fmt.Sprintf("msg:idempotent:%s", in.ClientMsgId)
		ok, err := l.svcCtx.Redis.SetNX(ctx, key, "1", consts.MsgIdempotentTTL).Result()
		if err != nil {
			l.Errorf("idempotent check failed: %v", err)
			return nil, ErrIdempotentCheckFailed
		} else if !ok {
			return nil, ErrDuplicateMessage
		}
	}

	// 2. 验证调用方身份：从 gRPC metadata 提取 user-id 并与请求中的 from_user_id 比对
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, ErrMetadataMissing
	}
	callerIDStrs := md.Get("user-id")
	if len(callerIDStrs) == 0 {
		return nil, ErrUserIDMissing
	}
	callerID, err := strconv.ParseInt(callerIDStrs[0], 10, 64)
	if err != nil {
		return nil, ErrInvalidUserID
	}
	if callerID != in.FromUserId {
		return nil, ErrSendAsOtherUser
	}

	// 3. 统一权限校验 (合并 IsMember+GetMembers+GetMuteStatus 为一个 gRPC 调用)
	if l.svcCtx.ConvClient == nil {
		return nil, ErrConversationUnavailable
	}
	isMember, isMuted, isMutedAll, muteUntil, convType, otherIDs, err := l.svcCtx.ConvClient.PreCheckSend(ctx, in.ConversationId, in.FromUserId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "precheck failed", err)
	}
	if !isMember {
		return nil, ErrNotMember
	}
	if isMutedAll || (isMuted && (muteUntil == 0 || time.Now().Unix() < muteUntil)) {
		return nil, ErrSenderMuted
	}
	// 私聊才需要拉黑校验 (群聊拉黑不阻止发消息)
	if convType == 1 && len(otherIDs) > 0 {
		blocked, err := l.svcCtx.FriendClient.IsBlockedAny(ctx, in.FromUserId, otherIDs)
		if err != nil {
			return nil, ErrBlockCheckFailed
		}
		if blocked {
			return nil, ErrBlockedByMember
		}
	}

	// 5. 构造消息
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

	// 6. 单事务：获取 seq + 写入消息 + outbox 事件 + 更新会话 max_seq
	senderName := resolveReplySenderName(ctx, l.svcCtx, msg.SenderID, "user")

	var seq int64
	err = l.svcCtx.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		seq, err = l.svcCtx.SequenceRepo.NextSeq(ctx, tx, in.ConversationId)
		if err != nil {
			return err
		}
		msg.Seq = seq

		if err := tx.Create(msg).Error; err != nil {
			return err
		}

		outboxID, err := l.svcCtx.Snowflake.Generate()
		if err != nil {
			return err
		}
		outboxEvent := &model.OutboxEvent{
			ID:         outboxID,
			Topic:      consts.KafkaTopicMessageCreated,
			Key:        fmt.Sprintf("%d", msgID),
			MaxRetries: model.DefaultMaxRetries,
		}
		if err := outboxEvent.SetPayload(buildMessageCreatedPayload(msg, senderName)); err != nil {
			return err
		}
		if err := l.svcCtx.OutboxRepo.Insert(ctx, tx, outboxEvent); err != nil {
			return err
		}

		// 同步更新会话的最新 seq
		return tx.Table("conv.conversations").Where("id = ?", in.ConversationId).
			Update("max_seq", seq).Error
	})
	if err != nil {
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
		"msg_type":        int64(msg.MsgType),
		"content":         msg.Content,
		"seq":             msg.Seq,
		"reply_to_msg_id": msg.ReplyToMsgID,
		"created_at":      msg.CreatedAt.Unix(),
		"sender_name":     senderName,
		"preview_text":    extractTextPreview(msg.MsgType, msg.Content),
	}
}

