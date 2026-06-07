package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		return nil, status.Error(codes.Internal, "failed to transfer owner")
	}
	if err := l.svcCtx.Repo.UpdateMemberRole(l.ctx, in.ConversationId, in.NewOwnerId, ownerRole); err != nil {
		l.Logger.Errorf("update new owner role failed: conv=%d user=%d err=%v", in.ConversationId, in.NewOwnerId, err)
	}
	if err := l.svcCtx.Repo.UpdateMemberRole(l.ctx, in.ConversationId, in.OperatorId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
		l.Logger.Errorf("demote old owner role failed: conv=%d user=%d err=%v", in.ConversationId, in.OperatorId, err)
	}
	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "conversation.owner.transferred", "转让了群主身份", []int64{in.NewOwnerId})

	l.Infof("owner transferred: conv=%d from=%d to=%d", in.ConversationId, in.OperatorId, in.NewOwnerId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
