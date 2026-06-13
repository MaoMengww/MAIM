package messageservicelogic

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type DeleteMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteMessageLogic {
	return &DeleteMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteMessageLogic) DeleteMessage(in *message.DeleteMessageReq) (*common.BaseResponse, error) {
	msgRepo := l.svcCtx.MessageRepo
	msg, err := msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	if msg.SenderID != in.UserId {
		return nil, ErrDeleteNotSender
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
		Topic:      consts.KafkaTopicMessageDeleted,
		Key:        fmt.Sprintf("delete:%d", in.MessageId),
		MaxRetries: model.DefaultMaxRetries,
	}
	if err := outboxEvent.SetPayload(map[string]any{
		"message_id":     in.MessageId,
		"conv_id":        convID,
		"user_id":        in.UserId,
		"delete_for_all": in.DeleteForAll,
	}); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "marshal outbox payload failed", err)
	}

	// 事务写: 删除/标记 + 插入 outbox
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if in.DeleteForAll {
			if err := msgRepo.DeleteWithTx(l.ctx, tx, in.MessageId); err != nil {
				return err
			}
		} else {
			if err := l.svcCtx.InboxRepo.MarkDeleted(l.ctx, in.UserId, convID, in.MessageId); err != nil {
				return err
			}
		}
		return l.svcCtx.OutboxRepo.Insert(l.ctx, tx, outboxEvent)
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "delete operation failed", err)
	}

	l.Infof("message deleted: msg_id=%d", in.MessageId)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
