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
	msgRepo := l.svcCtx.MessageRepo
	msg, err := msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	if msg.SenderID != in.UserId {
		return nil, ErrEditNotSender
	}

	window := time.Duration(l.svcCtx.Config.Message.EditWindowSeconds) * time.Second
	if time.Since(msg.CreatedAt) > window {
		return nil, ErrEditWindowExpired
	}

	if msg.MsgType != model.MsgTypeText {
		return nil, ErrEditNotText
	}

	editHistory := msg.EditHistory
	if editHistory == nil {
		editHistory = model.JSONArray{}
	}
	editHistory = append(editHistory, msg.Content)

	newContent := model.TextContent{
		Text:           in.Text.GetText(),
		MentionUserIDs: in.Text.GetMentionUserIds(),
		MentionAll:     in.Text.GetMentionAll(),
	}.ToJSONContent()

	editCount := msg.EditCount + 1
	if err := msgRepo.UpdateContent(l.ctx, in.MessageId, newContent, editHistory, editCount); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "update content failed", err)
	}

	kafkaMsg := map[string]any{
		"message_id":  in.MessageId,
		"conv_id":     in.ConversationId,
		"user_id":     in.UserId,
		"new_content": newContent,
	}
	kafkaVal, marshalErr := json.Marshal(kafkaMsg)
	if marshalErr != nil {
		l.Errorf("json marshal failed for message.edited: msg_id=%d, err=%v", in.MessageId, marshalErr)
	} else if err := l.svcCtx.MessageEditedProducer.Send(l.ctx, fmt.Sprintf("edit:%d", in.MessageId), kafkaVal); err != nil {
		l.Errorf("kafka produce message.edited failed: msg_id=%d, err=%v", in.MessageId, err)
	}

	metrics.MessageEditRecalledTotal.Inc("edit")

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
