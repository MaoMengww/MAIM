package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
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

	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	if err := requireRole(l.ctx, l.svcCtx.ConversationRepo, in.ConversationId, in.UserId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
		return nil, err
	}
	settings, err := l.svcCtx.ConversationRepo.GetSettings(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		l.Logger.Errorf("get settings failed: %v", err)
		return nil, err
	}
	return &conversation.GetSettingsResp{
		IsMuted:  settings.IsMuted,
		IsPinned: settings.IsPinned,
	}, nil
}
