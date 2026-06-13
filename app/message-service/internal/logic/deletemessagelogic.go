package logic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteMessageLogic {
	return &DeleteMessageLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteMessageLogic) DeleteMessage(in *message.DeleteMessageReq) (*common.BaseResponse, error) {
	// todo: add your logic here and delete this line

	return &common.BaseResponse{}, nil
}
