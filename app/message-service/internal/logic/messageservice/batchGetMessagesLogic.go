package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

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
	if in == nil || validateIdentities(in.MessageIds...) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid message identity")
	}
	if len(in.MessageIds) == 0 {
		return &message.BatchGetMessagesResp{Messages: []*message.Message{}}, nil
	}

	callerID := callerUserID(l.ctx)
	if callerID == "" {
		return nil, ErrUserIDMissing
	}
	msgRepo := l.svcCtx.MessageRepo.ForUser(callerID)
	msgs, err := msgRepo.GetByIDs(l.ctx, in.MessageIds)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "batch get messages failed", err)
	}

	var pbMsgs []*message.Message
	for i := range msgs {
		member, err := l.svcCtx.ConversationRepo.IsMember(l.ctx, msgs[i].ConvID, callerID)
		if err != nil {
			return nil, ErrMemberCheckFailed
		}
		if !member {
			continue
		}
		pbMsgs = append(pbMsgs, modelToPbMessage(&msgs[i]))
	}
	hydrateReplySummaries(l.ctx, msgRepo, l.svcCtx.ProfileRepo, l.svcCtx.ConversationRepo, pbMsgs)

	return &message.BatchGetMessagesResp{Messages: pbMsgs}, nil
}
