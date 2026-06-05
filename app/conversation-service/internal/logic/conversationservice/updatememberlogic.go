package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
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
	if in.Role != nil {
		if err := l.svcCtx.Repo.UpdateMemberRole(l.ctx, in.ConversationId, in.UserId, int32(in.GetRole())); err != nil {
			l.Logger.Errorf("update role failed: %v", err)
			return nil, err
		}
	}
	if in.Alias != nil {
		member, err := l.svcCtx.Repo.GetMember(l.ctx, in.ConversationId, in.UserId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrMemberNotFound
			}
			l.Logger.Errorf("get member failed: %v", err)
			return nil, err
		}
		member.Alias = in.GetAlias()
		if err := l.svcCtx.Repo.UpdateMember(l.ctx, member); err != nil {
			l.Logger.Errorf("update alias failed: %v", err)
			return nil, err
		}
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
