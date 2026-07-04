package messageservicelogic

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type SendSystemMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendSystemMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendSystemMessageLogic {
	return &SendSystemMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendSystemMessageLogic) SendSystemMessage(in *message.SendSystemMessageReq) (*message.SendMessageResp, error) {
	msgID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate msg id failed", err)
	}
	now := time.Now()

	sysContent := model.SystemContent{
		Action:         in.Action,
		Detail:         in.Detail,
		RelatedUserIDs: in.RelatedUserIds,
		ActorID:        in.ActorId,
		ActorType:      in.ActorType,
		Payload:        in.Payload,
	}

	msg := &model.Message{
		ID:          msgID,
		ConvID:      in.ConversationId,
		SenderID:    in.ActorId,
		MsgType:     model.MsgTypeSystem,
		Content:     sysContent.ToJSONContent(),
		Status:      model.MessageStatusNormal,
		EditHistory: model.JSONArray{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// 生成 seq + 插入消息 + outbox 事件 + 更新会话元数据（同一 PG 事务，原子提交）
	previewText := extractTextPreview(msg.MsgType, msg.Content)
	senderName := in.ActorType
	contentMap := map[string]any{
		"action":           in.Action,
		"detail":           in.Detail,
		"related_user_ids": in.RelatedUserIds,
		"actor_id":         in.ActorId,
		"actor_type":       in.ActorType,
		"payload":          in.Payload,
	}

	var seq int64
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		s, err := l.svcCtx.SequenceRepo.NextSeq(l.ctx, tx, in.ConversationId)
		if err != nil {
			return err
		}
		seq = s
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
		if err := outboxEvent.SetPayload(map[string]any{
			"message_id":  msgID,
			"conv_id":     in.ConversationId,
			"sender_id":   in.ActorId,
			"msg_type":    int64(model.MsgTypeSystem),
			"content":     contentMap,
			"seq":         seq,
			"created_at":  now.Unix(),
			"sender_name": senderName,
		}); err != nil {
			return err
		}
		if err := l.svcCtx.OutboxRepo.Insert(l.ctx, tx, outboxEvent); err != nil {
			return err
		}

		return tx.Table("conv.conversations").Where("id = ?", in.ConversationId).
			Updates(map[string]any{
				"max_seq":              seq,
				"last_message_id":      msgID,
				"last_message_preview": previewText,
				"updated_at":           time.Now(),
			}).Error
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "insert system message failed", err)
	}

	metrics.MessagesSentTotal.Inc("7")

	l.Infof("system message sent: msg_id=%d conv_id=%d action=%s", msgID, in.ConversationId, in.Action)

	return &message.SendMessageResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
