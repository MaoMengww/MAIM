package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type MuteAllLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMuteAllLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MuteAllLogic {
	return &MuteAllLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *MuteAllLogic) MuteAll(in *conversation.MuteAllReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.Repo.UpdateConversationMutedAll(l.ctx, in.ConversationId, true); err != nil {
		l.Logger.Errorf("mute all failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
