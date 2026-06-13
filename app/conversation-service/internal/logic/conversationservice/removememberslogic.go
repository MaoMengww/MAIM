package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/consts"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
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
	// Self-leave: any member can remove themselves from a conversation.
	allSelf := true
	for _, uid := range in.UserIds {
		if uid != in.OperatorId {
			allSelf = false
			break
		}
	}

	if !allSelf {
		if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
			return nil, err
		}
	}

	for _, uid := range in.UserIds {
		// Kicking others requires role check; self-removal does not.
		if uid != in.OperatorId {
			if err := verifyTargetNotHigher(l.ctx, l.svcCtx.Repo, in.ConversationId, uid, in.OperatorId); err != nil {
				return nil, err
			}
		}
		if err := l.svcCtx.Repo.RemoveMember(l.ctx, in.ConversationId, uid); err != nil {
			return nil, pkg_errors.Wrap(pkg_errors.CodeInternal, "failed to remove member", err)
		}
		if err := l.svcCtx.Repo.IncrementMemberCount(l.ctx, in.ConversationId, -1); err != nil {
			l.Logger.Errorf("decrement member count failed: conv=%d, err=%v", in.ConversationId, err)
		}
		// 发送成员退出系统消息
		action := consts.ConvActionMemberLeft
		detail := "成员退出了群聊"
		if uid == in.OperatorId {
			detail = "退出了群聊"
		}
		emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, uid, action, detail, []int64{uid})
	}
	l.Infof("members removed: conv_id=%d count=%d", in.ConversationId, len(in.UserIds))
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
