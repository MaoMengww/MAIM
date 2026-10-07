package messageservicelogic

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SendSystemMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendSystemMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendSystemMessageLogic {
	return &SendSystemMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendSystemMessageLogic) SendSystemMessage(in *message.SendSystemMessageReq) (*message.SendMessageResp, error) {
	if in == nil || validateIdentities(in.ConversationId) != nil || validateIdentities(in.RelatedUserIds...) != nil || (in.ActorId != nil && validateIdentities(*in.ActorId) != nil) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid system message identity")
	}
	msgID, err := identity.New()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate msg id failed", err)
	}
	now := time.Now()

	sysContent := model.SystemContent{
		Action:         in.Action,
		Detail:         in.Detail,
		RelatedUserIDs: in.RelatedUserIds,
		ActorID:        in.ActorId,
		ActorType:      in.ActorType,
		Payload:        in.Payload,
	}

	msg := &model.Message{
		ID:          msgID,
		ConvID:      in.ConversationId,
		SenderID:    in.ActorId,
		MsgType:     model.MsgTypeSystem,
		SenderType:  "system",
		Content:     sysContent.ToJSONContent(),
		Status:      model.MessageStatusNormal,
		EditHistory: model.JSONArray{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// 锁会话 + seq + 消息 + outbox + 最新消息（同一 PG 事务，原子提交）

	var seq int64
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		// 与普通消息/Bot回复保持一致：先锁会话，再分配 seq。
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", in.ConversationId).Take(&conv).Error; err != nil {
			return err
		}

		if err := persistMessage(l.ctx, l.svcCtx, tx, msg, in.ActorType); err != nil {
			return err
		}
		seq = msg.Seq
		return nil
	})
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New(errors.CodeNotFound, "conversation not found")
		}
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "insert system message failed", err)
	}

	metrics.MessagesSentTotal.Inc("7")

	l.Infof("system message sent: msg_id=%s conv_id=%s action=%s", msgID, in.ConversationId, in.Action)

	return &message.SendMessageResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
