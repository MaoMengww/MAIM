package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
)

type GetSettingsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetSettingsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetSettingsLogic {
	return &GetSettingsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetSettingsLogic) GetSettings(in *conversation.GetSettingsReq) (*conversation.GetSettingsResp, error) {
	settings, err := l.svcCtx.Repo.GetSettings(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		l.Logger.Errorf("get settings failed: %v", err)
		return nil, err
	}
	return &conversation.GetSettingsResp{
		IsMuted:  settings.IsMuted,
		IsPinned: settings.IsPinned,
	}, nil
}
