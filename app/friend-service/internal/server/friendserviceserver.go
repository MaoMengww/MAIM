package server

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/logic"
	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"
)

type FriendServiceServer struct {
	svcCtx *svc.ServiceContext
	friend.UnimplementedFriendServiceServer
}

func NewFriendServiceServer(svcCtx *svc.ServiceContext) *FriendServiceServer {
	return &FriendServiceServer{
		svcCtx: svcCtx,
	}
}

// ========== Friend Requests ==========
func (s *FriendServiceServer) SendRequest(ctx context.Context, in *friend.SendRequestReq) (*friend.SendRequestResp, error) {
	l := logic.NewSendRequestLogic(ctx, s.svcCtx)
	return l.SendRequest(in)
}

func (s *FriendServiceServer) AcceptRequest(ctx context.Context, in *friend.AcceptRequestReq) (*common.BaseResponse, error) {
	l := logic.NewAcceptRequestLogic(ctx, s.svcCtx)
	return l.AcceptRequest(in)
}

func (s *FriendServiceServer) RejectRequest(ctx context.Context, in *friend.RejectRequestReq) (*common.BaseResponse, error) {
	l := logic.NewRejectRequestLogic(ctx, s.svcCtx)
	return l.RejectRequest(in)
}

func (s *FriendServiceServer) CancelRequest(ctx context.Context, in *friend.CancelRequestReq) (*common.BaseResponse, error) {
	l := logic.NewCancelRequestLogic(ctx, s.svcCtx)
	return l.CancelRequest(in)
}

func (s *FriendServiceServer) ListPendingRequests(ctx context.Context, in *friend.ListPendingRequestsReq) (*friend.ListRequestsResp, error) {
	l := logic.NewListPendingRequestsLogic(ctx, s.svcCtx)
	return l.ListPendingRequests(in)
}

func (s *FriendServiceServer) ListSentRequests(ctx context.Context, in *friend.ListSentRequestsReq) (*friend.ListRequestsResp, error) {
	l := logic.NewListSentRequestsLogic(ctx, s.svcCtx)
	return l.ListSentRequests(in)
}

// ========== Friend Management ==========
func (s *FriendServiceServer) ListFriends(ctx context.Context, in *friend.ListFriendsReq) (*friend.ListFriendsResp, error) {
	l := logic.NewListFriendsLogic(ctx, s.svcCtx)
	return l.ListFriends(in)
}

func (s *FriendServiceServer) DeleteFriend(ctx context.Context, in *friend.DeleteFriendReq) (*common.BaseResponse, error) {
	l := logic.NewDeleteFriendLogic(ctx, s.svcCtx)
	return l.DeleteFriend(in)
}

func (s *FriendServiceServer) SetRemark(ctx context.Context, in *friend.SetRemarkReq) (*common.BaseResponse, error) {
	l := logic.NewSetRemarkLogic(ctx, s.svcCtx)
	return l.SetRemark(in)
}

func (s *FriendServiceServer) SetGroup(ctx context.Context, in *friend.SetGroupReq) (*common.BaseResponse, error) {
	l := logic.NewSetGroupLogic(ctx, s.svcCtx)
	return l.SetGroup(in)
}

// ========== Friend Groups ==========
func (s *FriendServiceServer) CreateGroup(ctx context.Context, in *friend.CreateGroupReq) (*friend.CreateGroupResp, error) {
	l := logic.NewCreateGroupLogic(ctx, s.svcCtx)
	return l.CreateGroup(in)
}

func (s *FriendServiceServer) RenameGroup(ctx context.Context, in *friend.RenameGroupReq) (*common.BaseResponse, error) {
	l := logic.NewRenameGroupLogic(ctx, s.svcCtx)
	return l.RenameGroup(in)
}

func (s *FriendServiceServer) DeleteGroup(ctx context.Context, in *friend.DeleteGroupReq) (*common.BaseResponse, error) {
	l := logic.NewDeleteGroupLogic(ctx, s.svcCtx)
	return l.DeleteGroup(in)
}

func (s *FriendServiceServer) ListGroups(ctx context.Context, in *friend.ListGroupsReq) (*friend.ListGroupsResp, error) {
	l := logic.NewListGroupsLogic(ctx, s.svcCtx)
	return l.ListGroups(in)
}

// ========== Blacklist ==========
func (s *FriendServiceServer) BlockUser(ctx context.Context, in *friend.BlockUserReq) (*common.BaseResponse, error) {
	l := logic.NewBlockUserLogic(ctx, s.svcCtx)
	return l.BlockUser(in)
}

func (s *FriendServiceServer) UnblockUser(ctx context.Context, in *friend.UnblockUserReq) (*common.BaseResponse, error) {
	l := logic.NewUnblockUserLogic(ctx, s.svcCtx)
	return l.UnblockUser(in)
}

func (s *FriendServiceServer) ListBlacklist(ctx context.Context, in *friend.ListBlacklistReq) (*friend.ListBlacklistResp, error) {
	l := logic.NewListBlacklistLogic(ctx, s.svcCtx)
	return l.ListBlacklist(in)
}

func (s *FriendServiceServer) IsBlocked(ctx context.Context, in *friend.IsBlockedReq) (*friend.IsBlockedResp, error) {
	l := logic.NewIsBlockedLogic(ctx, s.svcCtx)
	return l.IsBlocked(in)
}
