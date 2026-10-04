package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetUnreadCountsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetUnreadCountsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetUnreadCountsLogic {
	return &GetUnreadCountsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

// GetUnreadCounts returns the domain's canonical unread counts for the requested
// members. The push layer reads them here instead of keeping its own counter.
func (l *GetUnreadCountsLogic) GetUnreadCounts(in *conversation.GetUnreadCountsReq) (*conversation.GetUnreadCountsResp, error) {
	counts, err := l.svcCtx.ConversationRepo.UnreadCounts(l.ctx, in.ConversationId, in.UserIds)
	if err != nil {
		l.Errorf("unread counts failed: conv=%d err=%v", in.ConversationId, err)
		return nil, err
	}
	return &conversation.GetUnreadCountsResp{Counts: counts}, nil
}
