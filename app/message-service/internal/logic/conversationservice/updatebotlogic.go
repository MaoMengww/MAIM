package conversationservice

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type UpdateBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateBotLogic {
	return &UpdateBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateBotLogic) UpdateBot(in *conversation.UpdateBotReq) (*common.BaseResponse, error) {

	if err := validateRequest(l.ctx, in.OperatorId, in.ConversationId, in.BotId); err != nil {
		return nil, err
	}
	var settings map[string]any
	if in.BotSettings != "" {
		if err := json.Unmarshal([]byte(in.BotSettings), &settings); err != nil {
			return nil, ErrConvUpdateFailed
		}
	}
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
			return err
		}
		if _, err := r.GetBotInConv(l.ctx, conv.ID, in.BotId); err != nil {
			return err
		}
		if err := r.UpdateBot(l.ctx, conv.ID, in.BotId, settings); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
