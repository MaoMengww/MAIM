package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
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

	if err := validateRequest(l.ctx, in.OperatorId, in.ConversationId, in.UserId); err != nil {
		return nil, err
	}
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
			return err
		}
		if err := verifyTargetNotHigher(l.ctx, r, conv.ID, in.UserId, in.OperatorId); err != nil {
			return err
		}
		if err := r.UnmuteMember(l.ctx, conv.ID, in.UserId); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}

	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "member.unmuted", "被取消禁言", []string{in.UserId})

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
