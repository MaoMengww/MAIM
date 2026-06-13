package logic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/message-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetAroundSeqLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetAroundSeqLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetAroundSeqLogic {
	return &GetAroundSeqLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetAroundSeqLogic) GetAroundSeq(in *message.GetAroundSeqReq) (*message.GetMessagesResp, error) {
	// todo: add your logic here and delete this line

	return &message.GetMessagesResp{}, nil
}
