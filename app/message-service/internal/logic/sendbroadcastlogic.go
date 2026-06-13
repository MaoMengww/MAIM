package logic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/message-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

type SendBroadcastLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendBroadcastLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendBroadcastLogic {
	return &SendBroadcastLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// ========== Broadcast ==========
func (l *SendBroadcastLogic) SendBroadcast(in *message.SendBroadcastReq) (*message.SendBroadcastResp, error) {
	// todo: add your logic here and delete this line

	return &message.SendBroadcastResp{}, nil
}
