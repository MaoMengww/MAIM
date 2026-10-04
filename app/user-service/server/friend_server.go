package server

import (
	"context"

	friendlogic "github.com/maomeng/aim/app/user-service/internal/logic/friend"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"
)

// ========== Friend Requests ==========
func (s *UserServer) SendRequest(ctx context.Context, in *userpb.SendRequestReq) (*userpb.SendRequestResp, error) {
	l := friendlogic.NewSendRequestLogic(ctx, s.ctx.FriendContext)
	return l.SendRequest(in)
}

func (s *UserServer) AcceptRequest(ctx context.Context, in *userpb.AcceptRequestReq) (*common.BaseResponse, error) {
	l := friendlogic.NewAcceptRequestLogic(ctx, s.ctx.FriendContext)
	return l.AcceptRequest(in)
}

func (s *UserServer) RejectRequest(ctx context.Context, in *userpb.RejectRequestReq) (*common.BaseResponse, error) {
	l := friendlogic.NewRejectRequestLogic(ctx, s.ctx.FriendContext)
	return l.RejectRequest(in)
}

func (s *UserServer) CancelRequest(ctx context.Context, in *userpb.CancelRequestReq) (*common.BaseResponse, error) {
	l := friendlogic.NewCancelRequestLogic(ctx, s.ctx.FriendContext)
	return l.CancelRequest(in)
}

func (s *UserServer) ListPendingRequests(ctx context.Context, in *userpb.ListPendingRequestsReq) (*userpb.ListRequestsResp, error) {
	l := friendlogic.NewListPendingRequestsLogic(ctx, s.ctx.FriendContext)
	return l.ListPendingRequests(in)
}

func (s *UserServer) ListSentRequests(ctx context.Context, in *userpb.ListSentRequestsReq) (*userpb.ListRequestsResp, error) {
	l := friendlogic.NewListSentRequestsLogic(ctx, s.ctx.FriendContext)
	return l.ListSentRequests(in)
}

// ========== Friend Management ==========
func (s *UserServer) ListFriends(ctx context.Context, in *userpb.ListFriendsReq) (*userpb.ListFriendsResp, error) {
	l := friendlogic.NewListFriendsLogic(ctx, s.ctx.FriendContext)
	return l.ListFriends(in)
}

func (s *UserServer) DeleteFriend(ctx context.Context, in *userpb.DeleteFriendReq) (*common.BaseResponse, error) {
	l := friendlogic.NewDeleteFriendLogic(ctx, s.ctx.FriendContext)
	return l.DeleteFriend(in)
}

func (s *UserServer) SetRemark(ctx context.Context, in *userpb.SetRemarkReq) (*common.BaseResponse, error) {
	l := friendlogic.NewSetRemarkLogic(ctx, s.ctx.FriendContext)
	return l.SetRemark(in)
}

func (s *UserServer) SetGroup(ctx context.Context, in *userpb.SetGroupReq) (*common.BaseResponse, error) {
	l := friendlogic.NewSetGroupLogic(ctx, s.ctx.FriendContext)
	return l.SetGroup(in)
}

// ========== Friend Groups ==========
func (s *UserServer) CreateGroup(ctx context.Context, in *userpb.CreateGroupReq) (*userpb.CreateGroupResp, error) {
	l := friendlogic.NewCreateGroupLogic(ctx, s.ctx.FriendContext)
	return l.CreateGroup(in)
}

func (s *UserServer) RenameGroup(ctx context.Context, in *userpb.RenameGroupReq) (*common.BaseResponse, error) {
	l := friendlogic.NewRenameGroupLogic(ctx, s.ctx.FriendContext)
	return l.RenameGroup(in)
}

func (s *UserServer) DeleteGroup(ctx context.Context, in *userpb.DeleteGroupReq) (*common.BaseResponse, error) {
	l := friendlogic.NewDeleteGroupLogic(ctx, s.ctx.FriendContext)
	return l.DeleteGroup(in)
}

func (s *UserServer) ListGroups(ctx context.Context, in *userpb.ListGroupsReq) (*userpb.ListGroupsResp, error) {
	l := friendlogic.NewListGroupsLogic(ctx, s.ctx.FriendContext)
	return l.ListGroups(in)
}

// ========== Blacklist ==========
func (s *UserServer) BlockUser(ctx context.Context, in *userpb.BlockUserReq) (*common.BaseResponse, error) {
	l := friendlogic.NewBlockUserLogic(ctx, s.ctx.FriendContext)
	return l.BlockUser(in)
}

func (s *UserServer) UnblockUser(ctx context.Context, in *userpb.UnblockUserReq) (*common.BaseResponse, error) {
	l := friendlogic.NewUnblockUserLogic(ctx, s.ctx.FriendContext)
	return l.UnblockUser(in)
}

func (s *UserServer) ListBlacklist(ctx context.Context, in *userpb.ListBlacklistReq) (*userpb.ListBlacklistResp, error) {
	l := friendlogic.NewListBlacklistLogic(ctx, s.ctx.FriendContext)
	return l.ListBlacklist(in)
}

func (s *UserServer) IsBlocked(ctx context.Context, in *userpb.IsBlockedReq) (*userpb.IsBlockedResp, error) {
	l := friendlogic.NewIsBlockedLogic(ctx, s.ctx.FriendContext)
	return l.IsBlocked(in)
}
