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

type DeleteConversationLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteConversationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteConversationLogic {
	return &DeleteConversationLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *DeleteConversationLogic) DeleteConversation(in *conversation.DeleteConversationReq) (*common.BaseResponse, error) {

	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, in.ConversationId, in.UserId, ownerRole); err != nil {
			return err
		}
		var removed []string
		if err := tx.Model(&model.ConversationMember{}).
			Where("conv_id = ? AND member_type = ?", conv.ID, model.MemberTypeUser).
			Order("user_id").Pluck("user_id", &removed).Error; err != nil {
			return err
		}
		for _, child := range []any{&model.ConversationMember{}, &model.ConvBot{}, &model.ConvSettings{}, &model.ConvReadSeq{}} {
			if err := tx.Where("conv_id = ?", conv.ID).Delete(child).Error; err != nil {
				return err
			}
		}
		if err := publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, []string{}, removed); err != nil {
			return err
		}
		return r.DeleteConversation(l.ctx, conv.ID)
	})
	if err != nil {
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
