package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
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

	if err := validateRequest(l.ctx, in.OperatorId, in.ConversationId); err != nil {
		return nil, err
	}
	if err := validateRequest(l.ctx, in.OperatorId, in.UserIds...); err != nil {
		return nil, err
	}
	var removed []string
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if conv.Type == model.ConvTypeSystem {
			return pkg_errors.ErrForbidden
		}
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
			return err
		}
		seen := make(map[string]bool, len(in.UserIds))
		var members []*model.ConversationMember
		for _, uid := range in.UserIds {
			if seen[uid] {
				continue
			}
			seen[uid] = true
			if uid != in.OperatorId {
				if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
					return err
				}
				if err := verifyTargetNotHigher(l.ctx, r, conv.ID, uid, in.OperatorId); err != nil {
					return err
				}
			}
			member, err := r.GetMember(l.ctx, conv.ID, uid)
			if err != nil {
				return err
			}
			members = append(members, member)
			if member.MemberType == model.MemberTypeUser {
				removed = append(removed, uid)
			}
		}
		for _, member := range members {
			if err := r.RemoveMember(l.ctx, conv.ID, memberIdentity(member)); err != nil {
				return err
			}
		}
		if err := r.IncrementMemberCount(l.ctx, conv.ID, -len(members)); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, removed)
	})
	if err != nil {
		return nil, err
	}
	for _, uid := range removed {
		// 发送成员退出系统消息
		action := consts.ConvActionMemberLeft
		detail := "成员退出了群聊"
		if uid == in.OperatorId {
			detail = "退出了群聊"
		}
		emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, uid, action, detail, []string{uid})
	}
	l.Infof("members removed: conv_id=%s count=%d", in.ConversationId, len(in.UserIds))
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
