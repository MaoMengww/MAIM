package friend

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type SendRequestLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewSendRequestLogic(ctx context.Context, svcCtx *Context) *SendRequestLogic {
	return &SendRequestLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendRequestLogic) SendRequest(in *userpb.SendRequestReq) (*userpb.SendRequestResp, error) {
	fromUserID := userIDFromContext(l.ctx)
	if fromUserID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}
	toUserID := in.GetToUserId()
	if toUserID == 0 || fromUserID == toUserID {
		return nil, grpcError(ErrCannotFriendSelf)
	}

	if ok, _ := l.svcCtx.FriendRepo.IsFriend(l.ctx, fromUserID, toUserID); ok {
		return nil, grpcError(ErrAlreadyFriend)
	}
	if ok, _ := l.svcCtx.BlockRepo.IsBlocked(l.ctx, fromUserID, toUserID); ok {
		return nil, grpcError(ErrBlocked)
	}
	if ok, _ := l.svcCtx.FriendRequestRepo.CheckPending(l.ctx, fromUserID, toUserID); ok {
		return nil, grpcError(ErrRequestAlreadySent)
	}

	reqID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate request id failed: %w", err)
	}
	now := time.Now()
	req := &model.FriendRequest{
		ID:         reqID,
		FromUserID: fromUserID,
		ToUserID:   toUserID,
		Message:    in.GetMessage(),
		Status:     model.FriendRequestStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := l.svcCtx.FriendRequestRepo.Create(l.ctx, req); err != nil {
		l.Errorf("failed to send friend request: requester_id=%d addressee_id=%d err=%v", fromUserID, toUserID, err)
		return nil, grpcError(err)
	}

	l.Infof("friend request sent: requester_id=%d addressee_id=%d", fromUserID, toUserID)
	return &userpb.SendRequestResp{RequestId: reqID}, nil
}
