package messageservicelogic

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
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

	// 事务外预取 reply_to（涉及 DB 查询，不放在事务内）
	replyToMap := map[string]any{}
	if replyToID := in.GetReplyToId(); replyToID != 0 {
		replyToMap = buildReplyToMap(l.ctx, l.svcCtx, replyToID)
	}
	contentMap := map[string]any{
		"bot_id":      in.BotId,
		"bot_name":    botName,
		"bot_avatar":  botAvatar,
		"text":        in.Text,
		"raw_payload": in.RawPayload,
	}
	previewText := extractTextPreview(int32(model.MsgTypeBot), model.JSONContent{"text": in.Text})

	// 事务：生成 seq + 插入消息 + outbox 事件 + 更新会话 max_seq（原子提交）
	var seq int64
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		seq, err = l.svcCtx.SequenceRepo.NextSeq(l.ctx, tx, in.ConversationId)
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
			"preview_text":    previewText,
		}
		if len(replyToMap) > 0 {
			payload["reply_to"] = replyToMap
		}
		if err := outboxEvent.SetPayload(payload); err != nil {
			return err
		}
		if err := l.svcCtx.OutboxRepo.Insert(l.ctx, tx, outboxEvent); err != nil {
			return err
		}

		return tx.Table("conv.conversations").Where("id = ?", in.ConversationId).
			Update("max_seq", seq).Error
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "insert bot message failed", err)
	}

	l.Infof("bot reply sent: msg_id=%d bot_id=%d", msgID, in.BotId)

	return &message.SendBotReplyResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
