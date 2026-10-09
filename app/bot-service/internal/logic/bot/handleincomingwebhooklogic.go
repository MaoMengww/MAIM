package bot

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/run"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/zeromicro/go-zero/core/logx"
)

type HandleIncomingWebhookLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewHandleIncomingWebhookLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HandleIncomingWebhookLogic {
	return &HandleIncomingWebhookLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *HandleIncomingWebhookLogic) HandleIncomingWebhook(in *pb.WebhookReq) (*pb.RunResponse, error) {
	bot, err := l.resolveBot(in)
	if err != nil {
		return nil, err
	}
	if err := verifyWebhookSignature(in.Body, in.Signature, in.Timestamp, bot.WebhookSecret); err != nil {
		if err.Error() == "webhook timestamp expired" {
			return nil, ErrWebhookTimeout
		}
		return nil, ErrWebhookInvalid
	}
	receipt, err := run.NewService(l.svcCtx).ExecutorUpdate(l.ctx, bot.ID, in.Body)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	return &pb.RunResponse{RunJson: string(raw)}, nil
}

func (l *HandleIncomingWebhookLogic) resolveBot(in *pb.WebhookReq) (*botWithSecret, error) {
	if in.BotId != nil {
		if err := validateIDs(*in.BotId); err != nil {
			return nil, err
		}
		bot, err := l.svcCtx.Repo.GetBot(l.ctx, *in.BotId)
		if err != nil {
			return nil, ErrBotNotFound
		}
		if (bot.Status != "active" && !run.IsStopConfirmation(in.Body)) || bot.Type != "third_party" || bot.SubType != SubTypeWebhook || bot.WebhookSecret == "" {
			return nil, ErrBotForbidden
		}
		return &botWithSecret{ID: bot.ID, WebhookSecret: bot.WebhookSecret}, nil
	}
	bots, err := l.svcCtx.Repo.ListActiveWebhookBots(l.ctx)
	if err != nil {
		return nil, ErrBotNotFound
	}
	if run.IsStopConfirmation(in.Body) {
		if err := l.svcCtx.DB.WithContext(l.ctx).Table("bot.bots").Select("id, webhook_secret").Where("type = 'third_party' AND sub_type = ?", SubTypeWebhook).Find(&bots).Error; err != nil {
			return nil, ErrBotNotFound
		}
	}
	for _, bot := range bots {
		if verifyWebhookSignature(in.Body, in.Signature, in.Timestamp, bot.WebhookSecret) == nil {
			return &botWithSecret{ID: bot.ID, WebhookSecret: bot.WebhookSecret}, nil
		}
	}
	return nil, ErrWebhookInvalid
}

type botWithSecret struct {
	ID            string
	WebhookSecret string
}
