package messageservicelogic

import (
	"cmp"
	"context"
	"github.com/maomeng/aim/pkg/sequence"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetMessagesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMessagesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMessagesLogic {
	return &GetMessagesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetMessagesLogic) GetMessages(in *message.GetMessagesReq) (*message.GetMessagesResp, error) {
	if in == nil || validateIdentities(in.ConversationId, in.UserId) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid history identity")
	}
	if err := requireCaller(l.ctx, in.UserId); err != nil {
		return nil, err
	}
	limit := 30
	var cursor int64

	if in.Pagination != nil {
		if in.Pagination.Limit > 0 {
			limit = int(in.Pagination.Limit)
		}
		if sequence.Validate(in.Pagination.GetCursor()) != nil || in.Pagination.Limit < 0 {
			return nil, ErrInvalidSeq
		}
		cursor = in.Pagination.GetCursor()
	}

	maxPageSize := cmp.Or(l.svcCtx.Config.Message.MaxPageSize, 100)
	if limit > maxPageSize {
		limit = maxPageSize
	}

	var filterTypes []int32
	for _, t := range in.FilterTypes {
		filterTypes = append(filterTypes, int32(t))
	}

	var beforeTime, afterTime int64
	if in.BeforeTime != nil {
		beforeTime = *in.BeforeTime
	}
	if in.AfterTime != nil {
		afterTime = *in.AfterTime
	}

	// member check
	isMember, err := l.svcCtx.ConversationRepo.IsMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		return nil, ErrMemberCheckFailed
	}
	if !isMember {
		return nil, ErrNotMember
	}

	msgRepo := l.svcCtx.MessageRepo.ForUser(in.UserId)
	msgs, err := msgRepo.GetByConvID(l.ctx, in.ConversationId, limit+1, cursor, beforeTime, afterTime, filterTypes)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "get messages failed", err)
	}

	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}

	var pbMsgs []*message.Message
	for i := range msgs {
		pbMsgs = append(pbMsgs, modelToPbMessage(&msgs[i]))
	}
	hydrateReplySummaries(l.ctx, msgRepo, l.svcCtx.ProfileRepo, l.svcCtx.ConversationRepo, pbMsgs)

	resp := &message.GetMessagesResp{
		Messages: pbMsgs,
		Pagination: &message.MessagePaginationResp{
			HasMore: hasMore,
		},
	}

	if hasMore && len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		resp.Pagination.NextCursor = last.Seq
		resp.Pagination.Total = int64(len(pbMsgs))
	}

	return resp, nil
}
