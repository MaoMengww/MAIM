package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

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
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	maxPageSize := int32(l.svcCtx.Config.Message.MaxPageSize)
	if limit > maxPageSize {
		limit = maxPageSize
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

	inboxRepo := l.svcCtx.InboxRepo
	inboxes, err := inboxRepo.GetByUserAndConv(l.ctx, in.UserId, in.ConversationId, in.FromSeq, limit)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "sync inbox failed", err)
	}

	if len(inboxes) == 0 {
		maxSeq, err := inboxRepo.GetMaxSeq(l.ctx, in.UserId, in.ConversationId)
		if err != nil {
			return nil, errors.Wrap(errors.CodeInternal, "get max seq failed", err)
		}
		return &message.SyncMessagesResp{
			Messages: nil,
			HasMore:  false,
			MaxSeq:   maxSeq,
		}, nil
	}

	msgIDs := make([]int64, len(inboxes))
	for i, ib := range inboxes {
		msgIDs[i] = ib.MessageID
	}

	msgs, err := l.svcCtx.MessageRepo.GetByIDs(l.ctx, msgIDs)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "get messages failed", err)
	}

	msgMap := make(map[int64]model.Message, len(msgs))
	for _, m := range msgs {
		msgMap[m.ID] = m
	}

	var pbMsgs []*message.Message
	for _, ib := range inboxes {
		m, ok := msgMap[ib.MessageID]
		if !ok {
			continue
		}
		pbMsgs = append(pbMsgs, modelToPbMessage(&m))
	}
	hydrateReplySummaries(l.ctx, l.svcCtx.MessageRepo, l.svcCtx.UserClient, l.svcCtx.BotRepo, pbMsgs)

	lastSeq := inboxes[len(inboxes)-1].Seq

	return &message.SyncMessagesResp{
		Messages: pbMsgs,
		HasMore:  false,
		MaxSeq:   lastSeq,
	}, nil
}
