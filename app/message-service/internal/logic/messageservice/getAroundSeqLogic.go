package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

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
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}

	// member check
	if l.svcCtx.ConvClient == nil {
		return nil, ErrConversationUnavailable
	}
	isMember, err := l.svcCtx.ConvClient.IsMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		return nil, ErrMemberCheckFailed
	}
	if !isMember {
		return nil, ErrNotMember
	}

	msgRepo := l.svcCtx.MessageRepo
	msgs, err := msgRepo.GetAroundSeq(l.ctx, in.ConversationId, in.Seq, limit)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "get around seq failed", err)
	}

	var pbMsgs []*message.Message
	for i := range msgs {
		pbMsgs = append(pbMsgs, modelToPbMessage(&msgs[i]))
	}
	hydrateReplySummaries(l.ctx, l.svcCtx.MessageRepo, l.svcCtx.UserClient, l.svcCtx.BotRepo, pbMsgs)

	return &message.GetMessagesResp{Messages: pbMsgs}, nil
}
