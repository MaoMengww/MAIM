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

type TransferOwnerLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewTransferOwnerLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TransferOwnerLogic {
	return &TransferOwnerLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *TransferOwnerLogic) TransferOwner(in *conversation.TransferOwnerReq) (*common.BaseResponse, error) {
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, ownerRole); err != nil {
			return err
		}
		if _, err := r.GetMember(l.ctx, conv.ID, in.NewOwnerId); err != nil {
			return err
		}
		if in.NewOwnerId == in.OperatorId {
			return nil
		}
		if err := r.UpdateConversationOwner(l.ctx, conv.ID, in.NewOwnerId); err != nil {
			return err
		}
		if err := r.UpdateMemberRole(l.ctx, conv.ID, in.NewOwnerId, ownerRole); err != nil {
			return err
		}
		if err := r.UpdateMemberRole(l.ctx, conv.ID, in.OperatorId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "conversation.owner.transferred", "转让了群主身份", []int64{in.NewOwnerId})

	l.Infof("owner transferred: conv=%d from=%d to=%d", in.ConversationId, in.OperatorId, in.NewOwnerId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
