package conversationservice

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type MuteMemberLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMuteMemberLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MuteMemberLogic {
	return &MuteMemberLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *MuteMemberLogic) MuteMember(in *conversation.MuteMemberReq) (*common.BaseResponse, error) {

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
		muteUntil := int64(0)
		if in.DurationSeconds > 0 {
			muteUntil = time.Now().Unix() + in.DurationSeconds
		}
		if err := r.MuteMember(l.ctx, conv.ID, in.UserId, muteUntil); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}

	// 发送系统消息
	detail := "被禁言"
	if in.DurationSeconds > 0 {
		detail = fmt.Sprintf("被禁言 %d 秒", in.DurationSeconds)
	}
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "member.muted", detail, []string{in.UserId})

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
