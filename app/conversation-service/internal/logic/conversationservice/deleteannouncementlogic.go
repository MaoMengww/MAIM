package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteAnnouncementLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteAnnouncementLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteAnnouncementLogic {
	return &DeleteAnnouncementLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DeleteAnnouncementLogic) DeleteAnnouncement(in *conversation.DeleteAnnouncementReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.Repo.UpdateConversationAnnouncement(l.ctx, in.ConversationId, ""); err != nil {
		l.Logger.Errorf("delete announcement failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
