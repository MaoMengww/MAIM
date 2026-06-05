package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type UnmuteAllLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUnmuteAllLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UnmuteAllLogic {
	return &UnmuteAllLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UnmuteAllLogic) UnmuteAll(in *conversation.UnmuteAllReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.Repo.UpdateConversationMutedAll(l.ctx, in.ConversationId, false); err != nil {
		l.Logger.Errorf("unmute all failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
