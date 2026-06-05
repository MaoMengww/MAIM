package botplatform

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/zeromicro/go-zero/core/logx"
)

type IssueBotTokenLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewIssueBotTokenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *IssueBotTokenLogic {
	return &IssueBotTokenLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *IssueBotTokenLogic) IssueBotToken(in *pb.IssueBotTokenReq) (*pb.IssueBotTokenResp, error) {
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		l.Errorf("get bot failed: bot_id=%d err=%v", in.BotId, err)
		return nil, ErrBotNotFound
	}
	if bot.Status != "active" {
		return nil, ErrBotForbidden
	}

	token, err := l.svcCtx.BotJWT.GenerateBotToken(bot.ID, bot.OwnerID, bot.Type)
	if err != nil {
		l.Errorf("generate bot token failed: bot_id=%d err=%v", in.BotId, err)
		return nil, err
	}

	l.Infof("bot token issued: bot_id=%d", in.BotId)
	return &pb.IssueBotTokenResp{
		Token:     token,
		ExpiresAt: time.Now().Add(time.Duration(l.svcCtx.BotJWT.ExpireSeconds()) * time.Second).Unix(),
	}, nil
}
