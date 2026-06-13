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

	// 构造 outbox 事件
	outboxID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate outbox id failed", err)
	}
	outboxEvent := &model.OutboxEvent{
		ID:         outboxID,
		Topic:      consts.KafkaTopicMessageEdited,
		Key:        fmt.Sprintf("edit:%d", in.MessageId),
		MaxRetries: model.DefaultMaxRetries,
	}
	if err := outboxEvent.SetPayload(map[string]any{
		"message_id":  in.MessageId,
		"conv_id":     in.ConversationId,
		"user_id":     in.UserId,
		"new_content": newContent,
	}); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "marshal outbox payload failed", err)
	}

	// 事务写: 更新消息 + 插入 outbox
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if err := msgRepo.UpdateContentWithTx(l.ctx, tx, in.MessageId, newContent, editHistory, editCount); err != nil {
			return err
		}
		return l.svcCtx.OutboxRepo.Insert(l.ctx, tx, outboxEvent)
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "update content failed", err)
	}

	metrics.MessageEditRecalledTotal.Inc("edit")

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
