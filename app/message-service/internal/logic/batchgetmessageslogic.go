package logic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/message-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

type BatchGetMessagesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBatchGetMessagesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchGetMessagesLogic {
	return &BatchGetMessagesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *BatchGetMessagesLogic) BatchGetMessages(in *message.BatchGetMessagesReq) (*message.BatchGetMessagesResp, error) {
	// todo: add your logic here and delete this line

	return &message.BatchGetMessagesResp{}, nil
}
