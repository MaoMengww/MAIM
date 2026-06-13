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
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type RecallMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRecallMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RecallMessageLogic {
	return &RecallMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RecallMessageLogic) RecallMessage(in *message.RecallMessageReq) (*common.BaseResponse, error) {
	msgRepo := l.svcCtx.MessageRepo
	msg, err := msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	if msg.SenderID != in.UserId {
		return nil, ErrRecallNotSender
	}

	window := time.Duration(l.svcCtx.Config.Message.RecallWindowSeconds) * time.Second
	if time.Since(msg.CreatedAt) > window {
		return nil, ErrRecallWindowExpired
	}

	convID := in.ConversationId
	if convID == 0 {
		convID = msg.ConvID
	}

	// 构造 outbox 事件
	outboxID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate outbox id failed", err)
	}
	outboxEvent := &model.OutboxEvent{
		ID:         outboxID,
		Topic:      consts.KafkaTopicMessageRecalled,
		Key:        fmt.Sprintf("recall:%d", in.MessageId),
		MaxRetries: model.DefaultMaxRetries,
	}
	if err := outboxEvent.SetPayload(map[string]any{
		"message_id": in.MessageId,
		"conv_id":    convID,
		"user_id":    in.UserId,
	}); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "marshal outbox payload failed", err)
	}

	// 事务写: 更新状态 + 插入 outbox
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if err := msgRepo.UpdateStatusWithTx(l.ctx, tx, in.MessageId, model.MessageStatusRecalled); err != nil {
			return err
		}
		return l.svcCtx.OutboxRepo.Insert(l.ctx, tx, outboxEvent)
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "update status failed", err)
	}

	metrics.MessageEditRecalledTotal.Inc("recall")
	l.Infof("message recalled: msg_id=%d user_id=%d", in.MessageId, in.UserId)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
