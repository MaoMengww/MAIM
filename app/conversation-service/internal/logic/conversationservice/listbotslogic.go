package conversationservice

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
)

type ListBotsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListBotsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListBotsLogic {
	return &ListBotsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ListBotsLogic) ListBots(in *conversation.ListBotsReq) (*conversation.ListBotsResp, error) {
	cbs, err := l.svcCtx.Repo.ListBotsByConv(l.ctx, in.ConversationId)
	if err != nil {
		return nil, err
	}

	// Batch-fetch bot details for Name and Avatar.
	botIDs := make([]int64, len(cbs))
	for i, cb := range cbs {
		botIDs[i] = cb.BotID
	}
	botMap := make(map[int64]*model.Bot, len(botIDs))
	if bots, err := l.svcCtx.Repo.GetBotsByIDs(l.ctx, botIDs); err == nil {
		for i := range bots {
			botMap[bots[i].ID] = &bots[i]
		}
	}

	bots := make([]*conversation.BotInConv, 0, len(cbs))
	for _, cb := range cbs {
		settingsStr := ""
		if cb.BotSettings != nil {
			if compact, err := json.Marshal(cb.BotSettings); err == nil {
				settingsStr = string(compact)
			}
		}
		bot := botMap[cb.BotID]
		name := ""
		avatar := ""
		if bot != nil {
			name = bot.Name
			avatar = bot.Avatar
		}
		bots = append(bots, &conversation.BotInConv{
			BotId:       cb.BotID,
			Name:        name,
			Avatar:      avatar,
			BotSettings: settingsStr,
			AddedBy:     cb.AddedBy,
			AddedAt:     cb.CreatedAt.Unix(),
		})
	}
	l.Infof("bots listed: conv_id=%d count=%d", in.ConversationId, len(bots))
	return &conversation.ListBotsResp{Bots: bots}, nil
}
