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

type EditMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewEditMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *EditMessageLogic {
	return &EditMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *EditMessageLogic) EditMessage(in *message.EditMessageReq) (*common.BaseResponse, error) {
	if in == nil || validateIdentities(in.MessageId, in.UserId) != nil || (in.ConversationId != nil && validateIdentities(*in.ConversationId) != nil) || in.Text == nil || validateIdentities(in.Text.GetMentionUserIds()...) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid edit request")
	}
	if err := requireCaller(l.ctx, in.UserId); err != nil {
		return nil, err
	}
	msgRepo := l.svcCtx.MessageRepo
	msg, err := msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	if msg.SenderID == nil || *msg.SenderID != in.UserId {
		return nil, ErrEditNotSender
	}

	window := time.Duration(l.svcCtx.Config.Message.EditWindowSeconds) * time.Second
	if time.Since(msg.CreatedAt) > window {
		return nil, ErrEditWindowExpired
	}

	if msg.MsgType != model.MsgTypeText {
		return nil, ErrEditNotText
	}

	if in.ConversationId != nil && *in.ConversationId != msg.ConvID {
		return nil, errors.New(errors.CodeInvalidParam, "conversation does not match message")
	}
	newContent := model.TextContent{Text: in.Text.GetText(), MentionUserIDs: in.Text.GetMentionUserIds(),
		MentionAll: in.Text.GetMentionAll()}.ToJSONContent()
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockMutableMessage(l.ctx, l.svcCtx, tx, msg.ConvID, msg.ID, in.UserId)
		if err != nil {
			return err
		}
		if time.Since(current.CreatedAt) > window {
			return ErrEditWindowExpired
		}
		if current.Status == model.MessageStatusRecalled {
			return errors.New(errors.CodeForbidden, "recalled message cannot be edited")
		}
		history := append(current.EditHistory, current.Content)
		if err := msgRepo.UpdateContentWithTx(l.ctx, tx, current.ID, newContent, history, current.EditCount+1); err != nil {
			return err
		}
		current.Content, current.EditHistory = newContent, history
		current.EditCount++
		current.Status, current.UpdatedAt = model.MessageStatusEdited, time.Now()
		return publishMessageChange(l.ctx, l.svcCtx, tx, current, model.InboxMessageEdited)
	})
	if err != nil {
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "update content failed", err)
	}

	metrics.MessageEditRecalledTotal.Inc("edit")

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
