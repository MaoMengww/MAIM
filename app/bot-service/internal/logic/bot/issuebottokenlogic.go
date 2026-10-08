package bot

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
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
	if err := validateCaller(l.ctx, in.UserId, in.BotId); err != nil {
		return nil, err
	}
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		l.Errorf("get bot failed: bot_id=%s err=%v", in.BotId, err)
		return nil, ErrBotNotFound
	}
	if bot.Status != "active" || !canWrite(bot, in.UserId) {
		return nil, ErrBotForbidden
	}

	issuedAt := time.Now()
	token, err := l.svcCtx.BotJWT.GenerateBotToken(bot.ID, bot.OwnerType, bot.OwnerID, bot.Type)
	if err != nil {
		l.Errorf("generate bot token failed: bot_id=%s err=%v", in.BotId, err)
		return nil, err
	}
	if l.svcCtx.DB != nil {
		claims, err := l.svcCtx.BotJWT.ParseBotToken(token)
		if err != nil {
			return nil, err
		}
		if err := l.svcCtx.DB.WithContext(l.ctx).Exec("INSERT INTO bot.issued_tokens(token_id,bot_id,issued_at) VALUES (?,?,?)", claims.ID, bot.ID, issuedAt).Error; err != nil {
			return nil, err
		}
	}

	l.Infof("bot token issued: bot_id=%s", in.BotId)
	return &pb.IssueBotTokenResp{
		Token:     token,
		ExpiresAt: time.Now().Add(time.Duration(l.svcCtx.BotJWT.ExpireSeconds()) * time.Second).Unix(),
	}, nil
}
