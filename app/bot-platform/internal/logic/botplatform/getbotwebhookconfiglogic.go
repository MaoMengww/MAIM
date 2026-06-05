package botplatform

import (
	"context"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/zeromicro/go-zero/core/logx"
)

type GetBotWebhookConfigLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetBotWebhookConfigLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetBotWebhookConfigLogic {
	return &GetBotWebhookConfigLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetBotWebhookConfigLogic) GetBotWebhookConfig(in *pb.GetBotWebhookConfigReq) (*pb.GetBotWebhookConfigResp, error) {
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, err
	}
	return &pb.GetBotWebhookConfigResp{
		ConnMode:      bot.ConnMode,
		CallbackUrl:   bot.CallbackURL,
		WebhookSecret: bot.WebhookSecret,
		Status:        bot.Status,
		Type:          bot.Type,
	}, nil
}
