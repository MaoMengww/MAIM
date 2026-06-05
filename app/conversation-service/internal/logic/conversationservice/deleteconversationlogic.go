package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteConversationLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteConversationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteConversationLogic {
	return &DeleteConversationLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DeleteConversationLogic) DeleteConversation(in *conversation.DeleteConversationReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.UserId, ownerRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.Repo.DeleteConversation(l.ctx, in.ConversationId); err != nil {
		l.Logger.Errorf("delete conversation failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
