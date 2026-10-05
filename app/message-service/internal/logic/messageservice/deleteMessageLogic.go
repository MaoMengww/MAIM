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

	if in.ConversationId != 0 && in.ConversationId != msg.ConvID {
		return nil, errors.New(errors.CodeInvalidParam, "conversation does not match message")
	}
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if in.DeleteForAll {
			current, err := lockMutableMessage(l.ctx, l.svcCtx, tx, msg.ConvID, msg.ID, in.UserId)
			if err != nil {
				return err
			}
			if err := msgRepo.DeleteWithTx(l.ctx, tx, current.ID); err != nil {
				return err
			}
			return publishMessageChange(l.ctx, l.svcCtx, tx, current, model.InboxMessageDeleted)
		}
		// Personal deletion remains on the existing path until issue09.
		if err := l.svcCtx.InboxRepo.MarkDeleted(l.ctx, tx, in.UserId, msg.ConvID, in.MessageId); err != nil {
			return err
		}
		id, err := l.svcCtx.Snowflake.Generate()
		if err != nil {
			return err
		}
		evt := &model.OutboxEvent{ID: id, Topic: consts.KafkaTopicMessageDeleted,
			Key: fmt.Sprintf("%d", msg.ConvID), MaxRetries: model.DefaultMaxRetries}
		if err := evt.SetPayload(map[string]any{"message_id": msg.ID, "conv_id": msg.ConvID,
			"user_id": in.UserId, "delete_for_all": false}); err != nil {
			return err
		}
		return l.svcCtx.OutboxRepo.Insert(l.ctx, tx, evt)
	})
	if err != nil {
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "delete operation failed", err)
	}

	l.Infof("message deleted: msg_id=%d", in.MessageId)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
