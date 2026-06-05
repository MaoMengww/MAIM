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
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
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

	if err := msgRepo.UpdateStatus(l.ctx, in.MessageId, model.MessageStatusRecalled); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "update status failed", err)
	}

	convID := in.ConversationId
	if convID == 0 {
		convID = msg.ConvID
	}
	kafkaMsg := map[string]any{
		"message_id": in.MessageId,
		"conv_id":    convID,
		"user_id":    in.UserId,
	}
	kafkaVal, marshalErr := json.Marshal(kafkaMsg)
	if marshalErr != nil {
		l.Errorf("json marshal failed for message.recalled: msg_id=%d, err=%v", in.MessageId, marshalErr)
	} else if err := l.svcCtx.MessageRecalledProducer.Send(l.ctx, fmt.Sprintf("recall:%d", in.MessageId), kafkaVal); err != nil {
		l.Errorf("kafka produce message.recalled failed: msg_id=%d, err=%v", in.MessageId, err)
	}

	metrics.MessageEditRecalledTotal.Inc("recall")
	l.Infof("message recalled: msg_id=%d user_id=%d", in.MessageId, in.UserId)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
