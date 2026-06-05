package messageservicelogic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type ForwardMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewForwardMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ForwardMessageLogic {
	return &ForwardMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ForwardMessageLogic) ForwardMessage(in *message.ForwardMessageReq) (*message.ForwardMessageResp, error) {
	if len(in.MessageIds) == 0 {
		return nil, ErrForwardNoIDs
	}

	msgRepo := l.svcCtx.MessageRepo
	srcMsgs, err := msgRepo.GetByIDs(l.ctx, in.MessageIds)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "get source messages failed", err)
	}

	if len(srcMsgs) == 0 {
		return nil, ErrForwardSrcNotFound
	}

	now := time.Now()
	var newIDs []int64

	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		for _, src := range srcMsgs {
			seq, seqErr := nextSeq(l.svcCtx.Redis, l.ctx, in.TargetConversationId)
			if seqErr != nil {
				return seqErr
			}

			newID := l.svcCtx.Snowflake.Generate()
			newMsg := &model.Message{
				ID:           newID,
				ConvID:       in.TargetConversationId,
				SenderID:     in.FromUserId,
				Seq:          seq,
				MsgType:      src.MsgType,
				Content:      src.Content,
				ReplyToMsgID: src.ReplyToMsgID,
				Status:       model.MessageStatusNormal,
				EditHistory:  model.JSONArray{},
				CreatedAt:    now,
				UpdatedAt:    now,
			}

			if createErr := tx.WithContext(l.ctx).Create(newMsg).Error; createErr != nil {
				return createErr
			}

			newIDs = append(newIDs, newID)
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "forward messages failed", err)
	}

	kafkaMsg := map[string]any{
		"message_ids":            newIDs,
		"target_conversation_id": in.TargetConversationId,
		"from_user_id":           in.FromUserId,
	}
	kafkaVal, marshalErr := json.Marshal(kafkaMsg)
	if marshalErr != nil {
		l.Errorf("json marshal failed for message.created (forward): target_conv=%d, err=%v", in.TargetConversationId, marshalErr)
	} else if err := l.svcCtx.MessageCreatedProducer.Send(l.ctx, fmt.Sprintf("forward:%d", in.TargetConversationId), kafkaVal); err != nil {
		l.Errorf("kafka produce message.created (forward) failed: target_conv=%d, msg_ids=%v, err=%v", in.TargetConversationId, newIDs, err)
	}

	return &message.ForwardMessageResp{MessageIds: newIDs}, nil
}
