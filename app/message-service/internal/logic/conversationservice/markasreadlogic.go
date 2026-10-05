package conversationservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
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

	var lastReadSeq int64
	var advanced bool
	err := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		readID, err := l.svcCtx.Snowflake.Generate()
		if err != nil {
			return fmt.Errorf("generate read id failed: %w", err)
		}
		lastReadSeq, advanced, err = l.svcCtx.ConversationRepo.UpsertReadSeq(l.ctx, tx, in.ConversationId, userID, in.Seq, readID)
		if err != nil || !advanced {
			return err
		}
		return l.svcCtx.PublishInboxChange(l.ctx, tx, map[string]any{
			"kind":          "read.updated",
			"conv_id":       in.ConversationId,
			"user_id":       userID,
			"last_read_seq": lastReadSeq,
		})
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotMember
		}
		l.Errorf("mark as read failed: %v", err)
		return nil, err
	}

	l.Infof("marked as read: conv_id=%d user_id=%d seq=%d", in.ConversationId, userID, lastReadSeq)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
