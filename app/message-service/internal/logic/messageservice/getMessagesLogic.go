package messageservicelogic

import (
	"context"
	"strconv"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/pb/common"

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
	limit := 30
	var cursor int64

	if in.Pagination != nil {
		if in.Pagination.Limit > 0 {
			limit = int(in.Pagination.Limit)
		}
		if in.Pagination.Cursor != "" {
			c, err := strconv.ParseInt(in.Pagination.Cursor, 10, 64)
			if err != nil {
				return nil, errors.Wrap(errors.CodeInvalidParam, "invalid cursor", err)
			}
			cursor = c
		}
	}

	maxPageSize := l.svcCtx.Config.Message.MaxPageSize
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
	hydrateReplySummaries(l.ctx, l.svcCtx.MessageRepo, l.svcCtx.UserClient, l.svcCtx.BotRepo, pbMsgs)

	resp := &message.GetMessagesResp{
		Messages: pbMsgs,
		Pagination: &common.CursorPaginationResp{
			HasMore: hasMore,
		},
	}

	if hasMore && len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		resp.Pagination.NextCursor = strconv.FormatInt(last.Seq, 10)
		resp.Pagination.Total = int64(len(pbMsgs))
	}

	return resp, nil
}
