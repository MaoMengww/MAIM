package messageservicelogic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
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

	if in.DeleteForAll {
		if err := msgRepo.Delete(l.ctx, in.MessageId); err != nil {
			return nil, errors.Wrap(errors.CodeInternal, "delete message failed", err)
		}
	} else {
		inboxRepo := l.svcCtx.InboxRepo
		if err := inboxRepo.MarkDeleted(l.ctx, in.UserId, convID, in.MessageId); err != nil {
			return nil, errors.Wrap(errors.CodeInternal, "mark deleted failed", err)
		}
	}

	kafkaMsg := map[string]any{
		"message_id":     in.MessageId,
		"conv_id":        convID,
		"user_id":        in.UserId,
		"delete_for_all": in.DeleteForAll,
	}
	kafkaVal, marshalErr := json.Marshal(kafkaMsg)
	if marshalErr != nil {
		l.Errorf("json marshal failed for message.deleted: msg_id=%d, err=%v", in.MessageId, marshalErr)
	} else if err := l.svcCtx.MessageDeletedProducer.Send(l.ctx, fmt.Sprintf("delete:%d", in.MessageId), kafkaVal); err != nil {
		l.Errorf("kafka produce message.deleted failed: msg_id=%d, err=%v", in.MessageId, err)
	}

	l.Infof("message deleted: msg_id=%d", in.MessageId)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
