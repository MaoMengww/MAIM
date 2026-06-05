package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type RemoveBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRemoveBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RemoveBotLogic {
	return &RemoveBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *RemoveBotLogic) RemoveBot(in *conversation.RemoveBotReq) (*common.BaseResponse, error) {
	if err := l.svcCtx.Repo.RemoveBotWithMember(l.ctx, in.ConversationId, in.BotId); err != nil {
		return nil, ErrMemberRemoveFailed
	}
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "bot.removed", "机器人离开了群聊", nil)
	l.Infof("bot removed: conv_id=%d bot_id=%d", in.ConversationId, in.BotId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
