package messageservicelogic

import (
	"context"
	"encoding/json"
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
	msgID := l.svcCtx.Snowflake.Generate()
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

	// 6. 生成 seq + 插入消息
	seq, err := nextSeq(l.svcCtx.Redis, ctx, in.ConversationId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "next seq failed", err)
	}
	msg.Seq = seq
	if err := l.svcCtx.DB.WithContext(ctx).Create(msg).Error; err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "insert message failed", err)
	}

	// 7. 异步发送 Kafka 事件（不阻塞响应，失败写 failed_events 表兜底）
	senderName := resolveReplySenderName(ctx, l.svcCtx, msg.SenderID, "user")
	asyncSendKafka(l.ctx, l.svcCtx, msg.ID, in.ConversationId, msg.Seq, int64(msg.MsgType), contentJSON, msg.ReplyToMsgID, msg.CreatedAt.Unix(), senderName)

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
// asyncSendKafka 异步发送 Kafka 事件，不阻塞响应路径。
// Kafka 发送失败时写 failed_events 表，由后台 goroutine 重试。
func asyncSendKafka(ctx context.Context, svcCtx *svc.ServiceContext, msgID, convID, seq, msgType int64, contentJSON model.JSONContent, replyToMsgID int64, createdAt int64, senderName string) {
	payload := map[string]any{
		"message_id":      msgID,
		"conv_id":         convID,
		"sender_id":       0,
		"msg_type":        msgType,
		"content":         contentJSON,
		"seq":             seq,
		"reply_to_msg_id": replyToMsgID,
		"created_at":      createdAt,
		"sender_name":     senderName,
	}
	if replyToMsgID != 0 {
		payload["reply_to"] = buildReplyToMap(ctx, svcCtx, replyToMsgID)
	}
	val, _ := json.Marshal(payload)

	go func() {
		bCtx := context.WithoutCancel(ctx)
		producer := svcCtx.MessageCreatedProducer
		if err := producer.Send(bCtx, fmt.Sprintf("%d", msgID), val); err != nil {
			logx.WithContext(bCtx).Errorf("kafka async send failed, write to failed_events: msg_id=%d, err=%v", msgID, err)
			svcCtx.DB.WithContext(bCtx).Create(&model.FailedEvent{
				Topic:   "message.created",
				Key:     fmt.Sprintf("%d", msgID),
				Payload: val,
			})
		}
	}()
}

