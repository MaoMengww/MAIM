package conversationservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type UpdateSettingsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateSettingsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateSettingsLogic {
	return &UpdateSettingsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateSettingsLogic) UpdateSettings(in *conversation.UpdateSettingsReq) (*common.BaseResponse, error) {
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.UserId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
			return err
		}
		s, err := r.GetSettings(l.ctx, conv.ID, in.UserId)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			id, err := l.svcCtx.Snowflake.Generate()
			if err != nil {
				return fmt.Errorf("generate settings id failed: %w", err)
			}
			s = &model.ConvSettings{ID: id, ConvID: conv.ID, UserID: in.UserId}
		} else if err != nil {
			return err
		}
		if in.IsMuted != nil {
			s.IsMuted = in.GetIsMuted()
		}
		if in.IsPinned != nil {
			s.IsPinned = in.GetIsPinned()
		}
		if err := r.UpsertSettings(l.ctx, s); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, []int64{in.UserId}, nil)
	})
	if err != nil {
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
