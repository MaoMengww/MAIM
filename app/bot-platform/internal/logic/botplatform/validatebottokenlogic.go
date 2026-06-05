package botplatform

import (
	"context"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/zeromicro/go-zero/core/logx"
)

type ValidateBotTokenLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewValidateBotTokenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ValidateBotTokenLogic {
	return &ValidateBotTokenLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ValidateBotTokenLogic) ValidateBotToken(in *pb.ValidateBotTokenReq) (*pb.ValidateBotTokenResp, error) {
	claims, err := l.svcCtx.BotJWT.ParseBotToken(in.Token)
	if err != nil {
		l.Infof("bot token parse failed: err=%v", err)
		return &pb.ValidateBotTokenResp{Valid: false}, nil
	}

	bot, err := l.svcCtx.Repo.GetBot(l.ctx, claims.BotID)
	if err != nil || bot.Status != "active" {
		return &pb.ValidateBotTokenResp{Valid: false}, nil
	}

	return &pb.ValidateBotTokenResp{
		Valid:   true,
		BotId:   claims.BotID,
		OwnerId: claims.OwnerID,
		Type:    claims.Type,
	}, nil
}
