package messageservicelogic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
)

type SendBotReplyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendBotReplyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendBotReplyLogic {
	return &SendBotReplyLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendBotReplyLogic) SendBotReply(in *message.SendBotReplyReq) (*message.SendBotReplyResp, error) {
	if l.svcCtx.ConvClient == nil {
		return nil, ErrConversationUnavailable
	}
	isMember, err := l.svcCtx.ConvClient.IsMember(l.ctx, in.ConversationId, in.BotId)
	if err != nil {
		return nil, ErrMemberCheckFailed
	}
	if !isMember {
		return nil, ErrBotNotInConversation
	}

	msgID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate msg id failed", err)
	}
	now := time.Now()
	content := extractBotContent(in)

	// Look up bot name/avatar via gRPC (bot-platform) — 在事务外执行
	botName, botAvatar, _ := l.svcCtx.BotRepo.GetBotInfo(l.ctx, in.BotId)
	content.BotName = botName
	content.BotAvatar = botAvatar

	msg := &model.Message{
		ID:           msgID,
		ConvID:       in.ConversationId,
		SenderID:     in.BotId,
		MsgType:      model.MsgTypeBot,
		Content:      content.ToJSONContent(),
		ReplyToMsgID: in.GetReplyToId(),
		Status:       model.MessageStatusNormal,
		EditHistory:  model.JSONArray{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 生成 seq + 插入消息
	seq, err := nextSeq(l.svcCtx.Redis, l.ctx, in.ConversationId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "next seq failed", err)
	}
	msg.Seq = seq
	if err := l.svcCtx.DB.WithContext(l.ctx).Create(msg).Error; err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "insert bot message failed", err)
	}

	// 异步发送 Kafka 事件
	contentMap := map[string]any{
		"bot_id":      in.BotId,
		"bot_name":    botName,
		"bot_avatar":  botAvatar,
		"text":        in.Text,
		"raw_payload": in.RawPayload,
	}
	payload := map[string]any{
		"message_id":      msgID,
		"conv_id":         in.ConversationId,
		"sender_id":       in.BotId,
		"msg_type":        int64(model.MsgTypeBot),
		"content":         contentMap,
		"seq":             seq,
		"reply_to_msg_id": in.GetReplyToId(),
		"created_at":      now.Unix(),
		"sender_name":     botName,
		"preview_text":    extractTextPreview(int32(model.MsgTypeBot), model.JSONContent{"text": in.Text}),
	}
	if replyToID := in.GetReplyToId(); replyToID != 0 {
		payload["reply_to"] = buildReplyToMap(l.ctx, l.svcCtx, replyToID)
	}
	val, _ := json.Marshal(payload)

	go func() {
		producer := l.svcCtx.MessageCreatedProducer
		if err := producer.Send(context.Background(), fmt.Sprintf("%d", msgID), val); err != nil {
			l.Errorf("kafka async send failed, write to failed_events: msg_id=%d, err=%v", msgID, err)
			_ = l.svcCtx.DB.WithContext(context.Background()).Create(&model.FailedEvent{
				Topic:   "message.created",
				Key:     fmt.Sprintf("%d", msgID),
				Payload: val,
			}).Error
		}
	}()

	l.Infof("bot reply sent: msg_id=%d bot_id=%d", msgID, in.BotId)

	return &message.SendBotReplyResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
