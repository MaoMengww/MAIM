package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type UpdateSettingsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateSettingsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateSettingsLogic {
	return &UpdateSettingsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateSettingsLogic) UpdateSettings(in *conversation.UpdateSettingsReq) (*common.BaseResponse, error) {
	s := &model.ConvSettings{
		ID:     l.svcCtx.Snowflake.Generate(),
		ConvID: in.ConversationId,
		UserID: in.UserId,
	}
	if in.IsMuted != nil {
		s.IsMuted = in.GetIsMuted()
	}
	if in.IsPinned != nil {
		s.IsPinned = in.GetIsPinned()
	}
	if err := l.svcCtx.Repo.UpsertSettings(l.ctx, s); err != nil {
		l.Logger.Errorf("update settings failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
