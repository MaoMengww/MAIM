package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetMessageByIDLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMessageByIDLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMessageByIDLogic {
	return &GetMessageByIDLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetMessageByIDLogic) GetMessageByID(in *message.GetMessageByIDReq) (*message.GetMessageByIDResp, error) {
	msgRepo := l.svcCtx.MessageRepo
	msg, err := msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	pbMsg := modelToPbMessage(msg)
	hydrateReplySummaries(l.ctx, l.svcCtx.MessageRepo, l.svcCtx.UserClient, l.svcCtx.BotRepo, []*message.Message{pbMsg})
	return &message.GetMessageByIDResp{
		Message: pbMsg,
	}, nil
}
