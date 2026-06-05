package conversationservice

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type MuteMemberLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMuteMemberLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MuteMemberLogic {
	return &MuteMemberLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *MuteMemberLogic) MuteMember(in *conversation.MuteMemberReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := verifyTargetNotHigher(l.ctx, l.svcCtx.Repo, in.ConversationId, in.UserId, in.OperatorId); err != nil {
		return nil, err
	}
	muteUntil := int64(0)
	if in.DurationSeconds > 0 {
		muteUntil = time.Now().Unix() + in.DurationSeconds
	}
	if err := l.svcCtx.Repo.MuteMember(l.ctx, in.ConversationId, in.UserId, muteUntil); err != nil {
		l.Logger.Errorf("mute member failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
