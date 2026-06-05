package botplatform

import (
	"context"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type GetBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetBotLogic {
	return &GetBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetBotLogic) GetBot(in *pb.GetBotReq) (*pb.Bot, error) {
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrBotNotFound
		}
		l.Errorf("get bot failed: %v", err)
		return nil, errors.Wrap(errors.CodeDBError, "get bot failed", err)
	}
	l.Infof("bot fetched: bot_id=%d name=%s", bot.ID, bot.Name)
	return modelBotToProto(bot), nil
}
