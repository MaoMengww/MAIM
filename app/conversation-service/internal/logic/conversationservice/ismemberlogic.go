package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
)

type IsMemberLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewIsMemberLogic(ctx context.Context, svcCtx *svc.ServiceContext) *IsMemberLogic {
	return &IsMemberLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *IsMemberLogic) IsMember(in *conversation.IsMemberReq) (*conversation.IsMemberResp, error) {
	isMember, err := l.svcCtx.Repo.IsMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		l.Logger.Errorf("check member failed: %v", err)
		return nil, err
	}
	return &conversation.IsMemberResp{IsMember: isMember}, nil
}
