package conversationservice

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
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
	var settings map[string]any
	if in.BotSettings != "" {
		json.Unmarshal([]byte(in.BotSettings), &settings)
	}
	if err := l.svcCtx.Repo.UpdateBot(l.ctx, in.ConversationId, in.BotId, settings); err != nil {
		return nil, ErrConvUpdateFailed
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
