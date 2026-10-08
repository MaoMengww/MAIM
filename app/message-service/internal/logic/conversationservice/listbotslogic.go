package conversationservice

import (
	"context"
	"encoding/json"

	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
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

	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	if err := requireRole(l.ctx, l.svcCtx.ConversationRepo, in.ConversationId, in.UserId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
		return nil, err
	}
	cbs, err := l.svcCtx.ConversationRepo.ListBotsByConv(l.ctx, in.ConversationId)
	if err != nil {
		return nil, err
	}

	// Batch-fetch bot details for Name and Avatar.
	botIDs := make([]string, len(cbs))
	for i, cb := range cbs {
		botIDs[i] = cb.BotID
	}
	botMap := make(map[string]*botpb.Bot, len(botIDs))
	botDetails, err := l.svcCtx.ConversationRepo.GetBotsByIDs(l.ctx, botIDs)
	if err != nil {
		return nil, err
	}
	for _, bot := range botDetails {
		botMap[bot.Id] = bot
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
		ownerType := ""
		var ownerID *string
		if bot != nil {
			name = bot.Name
			avatar = bot.Avatar
			ownerType, ownerID = bot.OwnerType, bot.OwnerId
		}
		bots = append(bots, &conversation.BotInConv{
			BotId:       cb.BotID,
			BindingId:   cb.ID,
			OwnerType:   ownerType,
			OwnerId:     ownerID,
			Name:        name,
			Avatar:      avatar,
			BotSettings: settingsStr,
			AddedBy:     cb.AddedBy,
			AddedAt:     cb.CreatedAt.Unix(),
		})
	}
	l.Infof("bots listed: conv_id=%s count=%d", in.ConversationId, len(bots))
	return &conversation.ListBotsResp{Bots: bots}, nil
}
