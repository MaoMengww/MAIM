package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	friend "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
)

type FriendHandler struct {
	friendClient friend.UserServiceClient
}

func NewFriendHandler(conn grpc.ClientConnInterface) *FriendHandler {
	return &FriendHandler{friendClient: friend.NewUserServiceClient(conn)}
}

func (h *FriendHandler) SendRequest(c *gin.Context) {
	var req friend.SendRequestReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.FromUserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.friendClient.SendRequest(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) AcceptRequest(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &friend.AcceptRequestReq{RequestId: c.Param("id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.AcceptRequest(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) RejectRequest(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &friend.RejectRequestReq{RequestId: c.Param("id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.RejectRequest(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) CancelRequest(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &friend.CancelRequestReq{RequestId: c.Param("id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.CancelRequest(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListPendingRequests(c *gin.Context) {
	req := &friend.ListPendingRequestsReq{UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.ListPendingRequests(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListSentRequests(c *gin.Context) {
	req := &friend.ListSentRequestsReq{UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.ListSentRequests(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListFriends(c *gin.Context) {
	var req friend.ListFriendsReq
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.friendClient.ListFriends(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) DeleteFriend(c *gin.Context) {
	if !requirePathIdentities(c, "user_id") {
		return
	}
	req := &friend.DeleteFriendReq{FriendId: c.Param("user_id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.DeleteFriend(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) SetRemark(c *gin.Context) {
	if !requirePathIdentities(c, "user_id") {
		return
	}
	var req friend.SetRemarkReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.FriendId = c.Param("user_id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.friendClient.SetRemark(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) SetGroup(c *gin.Context) {
	if !requirePathIdentities(c, "user_id") {
		return
	}
	var req friend.SetGroupReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.FriendId = c.Param("user_id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.friendClient.SetGroup(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) CreateGroup(c *gin.Context) {
	var req friend.CreateGroupReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.friendClient.CreateGroup(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) RenameGroup(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req friend.RenameGroupReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.GroupId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.friendClient.RenameGroup(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) DeleteGroup(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &friend.DeleteGroupReq{GroupId: c.Param("id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.DeleteGroup(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListGroups(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListGroups(ctx, &friend.ListGroupsReq{UserId: c.GetString(middleware.CtxKeyUserID)})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) BlockUser(c *gin.Context) {
	if !requirePathIdentities(c, "user_id") {
		return
	}
	req := &friend.BlockUserReq{BlockedUserId: c.Param("user_id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.BlockUser(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) UnblockUser(c *gin.Context) {
	if !requirePathIdentities(c, "user_id") {
		return
	}
	req := &friend.UnblockUserReq{BlockedUserId: c.Param("user_id"), UserId: c.GetString(middleware.CtxKeyUserID)}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.friendClient.UnblockUser(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListBlacklist(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListBlacklist(ctx, &friend.ListBlacklistReq{UserId: c.GetString(middleware.CtxKeyUserID), Pagination: &common.Pagination{}})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}
