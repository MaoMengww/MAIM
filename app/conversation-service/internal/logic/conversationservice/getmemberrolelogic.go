package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type GetMemberRoleLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMemberRoleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMemberRoleLogic {
	return &GetMemberRoleLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetMemberRoleLogic) GetMemberRole(in *conversation.GetMemberRoleReq) (*conversation.GetMemberRoleResp, error) {
	member, err := l.svcCtx.Repo.GetMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMemberNotFound
		}
		l.Logger.Errorf("get member failed: %v", err)
		return nil, err
	}
	return &conversation.GetMemberRoleResp{Role: conversation.MemberRole(member.Role)}, nil
}
