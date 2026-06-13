package messageservicelogic

import (
	"context"
	"encoding/json"
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

	// 生成 seq + 插入消息 + 更新会话元数据（同一 PG 事务）
	previewText := extractTextPreview(msg.MsgType, msg.Content)
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

	// 异步发送 Kafka 事件
	senderName := in.ActorType
	senderID := in.ActorId
	contentMap := map[string]any{
		"action":           in.Action,
		"detail":           in.Detail,
		"related_user_ids": in.RelatedUserIds,
		"actor_id":         in.ActorId,
		"actor_type":       in.ActorType,
		"payload":          in.Payload,
	}
	payload := map[string]any{
		"message_id":  msgID,
		"conv_id":     in.ConversationId,
		"sender_id":   senderID,
		"msg_type":    int64(model.MsgTypeSystem),
		"content":     contentMap,
		"seq":         seq,
		"created_at":  now.Unix(),
		"sender_name": senderName,
	}
	val, _ := json.Marshal(payload)

	go func() {
		producer := l.svcCtx.MessageCreatedProducer
		if err := producer.Send(context.Background(), fmt.Sprintf("%d", msgID), val); err != nil {
			l.Errorf("kafka async send failed, write to failed_events: msg_id=%d, err=%v", msgID, err)
			_ = l.svcCtx.DB.WithContext(context.Background()).Create(&model.FailedEvent{
				Topic:   consts.KafkaTopicMessageCreated,
				Key:     fmt.Sprintf("%d", msgID),
				Payload: val,
			}).Error
		}
	}()

	l.Infof("system message sent: msg_id=%d conv_id=%d action=%s", msgID, in.ConversationId, in.Action)

	return &message.SendMessageResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
