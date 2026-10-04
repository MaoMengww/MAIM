package conversationservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type MarkAsReadLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMarkAsReadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MarkAsReadLogic {
	return &MarkAsReadLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *MarkAsReadLogic) MarkAsRead(in *conversation.MarkAsReadReq) (*common.BaseResponse, error) {
	// Extract user ID from gRPC context (set by UnaryUserIDInterceptor)
	userID := in.UserId
	if uid, ok := l.ctx.Value(interceptor.ContextKeyUserID).(int64); ok {
		userID = uid
	}

	readID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate read id failed: %w", err)
	}
	lastReadSeq, err := l.svcCtx.ConversationRepo.UpsertReadSeq(l.ctx, in.ConversationId, userID, in.Seq, readID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotMember
		}
		l.Errorf("mark as read failed: %v", err)
		return nil, err
	}

	// 已读位点即未读读模型的输入：位置推进后未读数由本地查询重新得出，
	// 因此这里不再需要清理任何缓存。

	// Produce conversation.read.updated event
	if l.svcCtx.ReadUpdatedProducer != nil {
		evtData, _ := json.Marshal(event.ConversationReadUpdatedEvent{
			ConvID:      in.ConversationId,
			UserID:      userID,
			LastReadSeq: lastReadSeq,
		})
		if err := l.svcCtx.ReadUpdatedProducer.Send(l.ctx, fmt.Sprintf("%d", in.ConversationId), evtData); err != nil {
			l.Errorf("produce conversation.read.updated failed: %v", err)
		}
	}

	l.Infof("marked as read: conv_id=%d user_id=%d seq=%d", in.ConversationId, userID, lastReadSeq)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
