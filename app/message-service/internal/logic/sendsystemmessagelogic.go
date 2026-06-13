package logic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/message-service/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
)

type SendSystemMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendSystemMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendSystemMessageLogic {
	return &SendSystemMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendSystemMessageLogic) SendSystemMessage(in *message.SendSystemMessageReq) (*message.SendMessageResp, error) {
	// todo: add your logic here and delete this line

	return &message.SendMessageResp{}, nil
}
