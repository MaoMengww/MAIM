package botplatform

import (
	"context"

	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
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
