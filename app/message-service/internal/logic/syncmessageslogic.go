package logic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/message-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

type SyncMessagesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSyncMessagesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SyncMessagesLogic {
	return &SyncMessagesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SyncMessagesLogic) SyncMessages(in *message.SyncMessagesReq) (*message.SyncMessagesResp, error) {
	// todo: add your logic here and delete this line

	return &message.SyncMessagesResp{}, nil
}
