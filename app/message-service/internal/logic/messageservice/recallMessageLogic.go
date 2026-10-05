package messageservicelogic

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
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

	if in.ConversationId != 0 && in.ConversationId != msg.ConvID {
		return nil, errors.New(errors.CodeInvalidParam, "conversation does not match message")
	}
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockMutableMessage(l.ctx, l.svcCtx, tx, msg.ConvID, msg.ID, in.UserId)
		if err != nil {
			return err
		}
		if time.Since(current.CreatedAt) > window {
			return ErrRecallWindowExpired
		}
		if current.Status == model.MessageStatusRecalled {
			return nil
		}
		if err := msgRepo.UpdateStatusWithTx(l.ctx, tx, current.ID, model.MessageStatusRecalled); err != nil {
			return err
		}
		current.Status, current.UpdatedAt = model.MessageStatusRecalled, time.Now()
		return publishMessageChange(l.ctx, l.svcCtx, tx, current, model.InboxMessageRecalled)
	})
	if err != nil {
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "update status failed", err)
	}

	metrics.MessageEditRecalledTotal.Inc("recall")
	l.Infof("message recalled: msg_id=%d user_id=%d", in.MessageId, in.UserId)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
