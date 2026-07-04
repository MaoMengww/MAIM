package botplatform

import (
	"context"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/zeromicro/go-zero/core/logx"
)

type RotateSecretLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRotateSecretLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RotateSecretLogic {
	return &RotateSecretLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *RotateSecretLogic) RotateSecret(in *pb.RotateSecretReq) (*pb.RotateSecretResp, error) {
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		return nil, ErrBotNotFound
	}
	if in.UserId != 0 && bot.OwnerID != 0 && in.UserId != bot.OwnerID {
		return nil, ErrBotForbidden
	}

	webhookSecret, err := generateRandomSecret(32)
	if err != nil {
		return nil, err
	}
	appSecret, err := generateRandomSecret(32)
	if err != nil {
		return nil, err
	}

	updates := map[string]any{
		"webhook_secret":  webhookSecret,
		"app_secret_hash": hashSecret(appSecret),
	}
	if err := l.svcCtx.Repo.UpdateBot(l.ctx, in.BotId, updates); err != nil {
		return nil, err
	}

	l.Infof("bot secret rotated: bot_id=%d", in.BotId)
	return &pb.RotateSecretResp{
		WebhookSecret: webhookSecret,
		AppSecret:     appSecret,
	}, nil
}
