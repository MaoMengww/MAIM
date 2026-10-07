package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type UpdateMemberLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateMemberLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateMemberLogic {
	return &UpdateMemberLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateMemberLogic) UpdateMember(in *conversation.UpdateMemberReq) (*common.BaseResponse, error) {

	if err := validateRequest(l.ctx, in.OperatorId, in.ConversationId, in.UserId); err != nil {
		return nil, err
	}
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if conv.Type == model.ConvTypeSystem {
			return pkg_errors.ErrForbidden
		}
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
			return err
		}
		member, err := r.GetMember(l.ctx, conv.ID, in.UserId)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		if in.Role != nil {
			if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, ownerRole); err != nil {
				return err
			}
			role := int32(in.GetRole())
			if member.Role == ownerRole || role < adminRole || role > int32(conversation.MemberRole_MEMBER_ROLE_MEMBER) {
				return pkg_errors.New(pkg_errors.CodeForbidden, "ownership changes require transfer owner")
			}
			member.Role = role
		}
		if in.Alias != nil {
			if in.UserId != in.OperatorId {
				if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
					return err
				}
				if err := verifyTargetNotHigher(l.ctx, r, conv.ID, in.UserId, in.OperatorId); err != nil {
					return err
				}
			}
			member.Alias = in.GetAlias()
		}
		if err := r.UpdateMember(l.ctx, member); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
