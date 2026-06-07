package conversationservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
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
	if err := l.svcCtx.Repo.UpsertReadSeq(l.ctx, in.ConversationId, userID, in.Seq, readID); err != nil {
		l.Errorf("mark as read failed: %v", err)
		return nil, err
	}

	// Clear Redis unread cache so ListConversations returns correct count
	if l.svcCtx.UnreadCache != nil {
		if err := l.svcCtx.UnreadCache.ClearUnreadCount(l.ctx, userID, in.ConversationId); err != nil {
			l.Errorf("clear unread cache failed: %v", err)
		}
	}

	// Produce conversation.read.updated event
	if l.svcCtx.UnreadProducer != nil {
		evtData, _ := json.Marshal(event.ConversationReadUpdatedEvent{
			ConvID:      in.ConversationId,
			UserID:      userID,
			LastReadSeq: in.Seq,
		})
		if err := l.svcCtx.UnreadProducer.Send(l.ctx, fmt.Sprintf("%d", in.ConversationId), evtData); err != nil {
			l.Errorf("produce conversation.read.updated failed: %v", err)
		}
	}

	l.Infof("marked as read: conv_id=%d user_id=%d", in.ConversationId, in.UserId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
