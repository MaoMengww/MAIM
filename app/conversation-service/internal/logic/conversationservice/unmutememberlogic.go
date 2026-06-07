package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type UnmuteMemberLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUnmuteMemberLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UnmuteMemberLogic {
	return &UnmuteMemberLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UnmuteMemberLogic) UnmuteMember(in *conversation.UnmuteMemberReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := verifyTargetNotHigher(l.ctx, l.svcCtx.Repo, in.ConversationId, in.UserId, in.OperatorId); err != nil {
		return nil, err
	}
	if err := l.svcCtx.Repo.UnmuteMember(l.ctx, in.ConversationId, in.UserId); err != nil {
		l.Logger.Errorf("unmute member failed: %v", err)
		return nil, err
	}

	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "member.unmuted", "被取消禁言", []int64{in.UserId})

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
