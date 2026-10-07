package friend

import (
	"context"
	"fmt"
	"github.com/maomeng/aim/pkg/identity"
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
	if err := validateCaller(l.ctx, in.GetFromUserId(), in.GetToUserId()); err != nil {
		return nil, err
	}
	fromUserID := userIDFromContext(l.ctx)
	if fromUserID == "" {
		return nil, grpcError(ErrUnauthenticated)
	}
	toUserID := in.GetToUserId()
	if identity.Validate(toUserID) != nil || fromUserID == toUserID {
		return nil, grpcError(ErrCannotFriendSelf)
	}

	if _, err := l.svcCtx.UserLogic.GetUserInfo(l.ctx, &userpb.GetUserInfoReq{UserId: toUserID}); err != nil {
		return nil, err
	}
	if ok, err := l.svcCtx.FriendRepo.IsFriend(l.ctx, fromUserID, toUserID); err != nil {
		return nil, grpcError(err)
	} else if ok {
		return nil, grpcError(ErrAlreadyFriend)
	}
	if ok, err := l.svcCtx.BlockRepo.IsBlocked(l.ctx, fromUserID, toUserID); err != nil {
		return nil, grpcError(err)
	} else if ok {
		return nil, grpcError(ErrBlocked)
	}
	if ok, err := l.svcCtx.FriendRequestRepo.CheckPending(l.ctx, fromUserID, toUserID); err != nil {
		return nil, grpcError(err)
	} else if ok {
		return nil, grpcError(ErrRequestAlreadySent)
	}

	reqID, err := identity.New()
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
		l.Errorf("failed to send friend request: requester_id=%s addressee_id=%s err=%v", fromUserID, toUserID, err)
		return nil, grpcError(err)
	}

	l.Infof("friend request sent: requester_id=%s addressee_id=%s", fromUserID, toUserID)
	return &userpb.SendRequestResp{RequestId: reqID}, nil
}
