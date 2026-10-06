package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if in.UserId <= 0 || in.MessageId <= 0 {
		return nil, errors.New(errors.CodeInvalidParam, "invalid deletion request")
	}
	msgRepo := l.svcCtx.MessageRepo
	msg, err := msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	if in.DeleteForAll && msg.SenderID != in.UserId {
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
		permission, err := l.svcCtx.ConversationRepo.CheckSendPermission(l.ctx, tx, msg.ConvID, in.UserId)
		if err != nil {
			return err
		}
		if !permission.IsMember {
			return ErrNotMember
		}
		var current model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conv_id = ?", msg.ID, msg.ConvID).Take(&current).Error; err != nil {
			return err
		}
		inserted, err := msgRepo.InsertPersonalDeletion(l.ctx, tx, in.UserId, current.ConvID, current.ID)
		if err != nil || !inserted {
			return err
		}
		// A tombstone contains no content and targets only the actor's account.
		return l.svcCtx.PublishInboxChange(l.ctx, tx, map[string]any{
			"kind": model.InboxMessageDeleted, "conv_id": current.ConvID, "message_id": current.ID,
			"user_id": in.UserId, "recipient_ids": []int64{in.UserId},
		})
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
