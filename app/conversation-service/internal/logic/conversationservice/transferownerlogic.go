package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type TransferOwnerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewTransferOwnerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TransferOwnerLogic {
	return &TransferOwnerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *TransferOwnerLogic) TransferOwner(in *conversation.TransferOwnerReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, ownerRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.Repo.UpdateConversationOwner(l.ctx, in.ConversationId, in.NewOwnerId); err != nil {
		l.Logger.Errorf("transfer owner failed: %v", err)
		return nil, err
	}
	l.svcCtx.Repo.UpdateMemberRole(l.ctx, in.ConversationId, in.NewOwnerId, ownerRole)
	l.svcCtx.Repo.UpdateMemberRole(l.ctx, in.ConversationId, in.OperatorId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER))
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
