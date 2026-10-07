package conversationservice

import (
	"context"
	"errors"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	pkg_errors "github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type UpdateConversationLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateConversationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateConversationLogic {
	return &UpdateConversationLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateConversationLogic) UpdateConversation(in *conversation.UpdateConversationReq) (*common.BaseResponse, error) {

	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if conv.Type != int32(conversation.ConversationType_CONVERSATION_TYPE_GROUP) {
			return pkg_errors.New(pkg_errors.CodeForbidden, "only group conversations can be updated")
		}
		if err := requireRole(l.ctx, r, in.ConversationId, in.UserId, adminRole); err != nil {
			return err
		}
		if in.Name != nil {
			conv.Name = in.GetName()
		}
		if in.Avatar != nil {
			conv.Avatar = in.GetAvatar()
		}
		if in.Background != nil {
			conv.Background = in.GetBackground()
		}
		conv.UpdatedAt = time.Now()
		if err := r.UpdateConversation(l.ctx, conv); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, in.ConversationId, nil, nil)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrConvNotFound
	}
	if err != nil {
		return nil, err
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
