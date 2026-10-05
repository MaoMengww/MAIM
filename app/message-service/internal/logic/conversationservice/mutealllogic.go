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

type MuteAllLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMuteAllLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MuteAllLogic {
	return &MuteAllLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *MuteAllLogic) MuteAll(in *conversation.MuteAllReq) (*common.BaseResponse, error) {
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
			return err
		}
		if err := r.UpdateConversationMutedAll(l.ctx, conv.ID, true); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}

	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "conversation.muted_all", "开启了全员禁言", nil)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
