package bot

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/zeromicro/go-zero/core/logx"
)

type BatchGetBotsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBatchGetBotsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchGetBotsLogic {
	return &BatchGetBotsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *BatchGetBotsLogic) BatchGetBots(in *pb.BatchGetBotsReq) (*pb.BatchGetBotsResp, error) {
	if err := validateIDs(in.BotIds...); err != nil {
		return nil, err
	}
	bots, err := l.svcCtx.Repo.GetBotsByIDs(l.ctx, in.BotIds)
	if err != nil {
		return nil, err
	}
	pbBots := make([]*pb.Bot, len(bots))
	for i := range bots {
		pbBots[i] = modelBotToProto(&bots[i])
	}
	return &pb.BatchGetBotsResp{Bots: pbBots}, nil
}
