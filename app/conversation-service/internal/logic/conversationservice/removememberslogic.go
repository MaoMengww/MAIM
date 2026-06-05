package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type RemoveMembersLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRemoveMembersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RemoveMembersLogic {
	return &RemoveMembersLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *RemoveMembersLogic) RemoveMembers(in *conversation.RemoveMembersReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	for _, uid := range in.UserIds {
		if err := verifyTargetNotHigher(l.ctx, l.svcCtx.Repo, in.ConversationId, uid, in.OperatorId); err != nil {
			l.Logger.Errorf("remove member %d denied: %v", uid, err)
			continue
		}
		if err := l.svcCtx.Repo.RemoveMember(l.ctx, in.ConversationId, uid); err != nil {
			l.Logger.Errorf("remove member %d failed: %v", uid, err)
		}
		if err := l.svcCtx.Repo.IncrementMemberCount(l.ctx, in.ConversationId, -1); err != nil {
			l.Logger.Errorf("decrement member count failed: conv=%d, err=%v", in.ConversationId, err)
		}
	}
	l.Infof("members removed: conv_id=%d count=%d", in.ConversationId, len(in.UserIds))
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
