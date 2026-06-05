package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
)

type FriendHandler struct {
	friendClient friend.FriendServiceClient
}

func NewFriendHandler(conn grpc.ClientConnInterface) *FriendHandler {
	return &FriendHandler{friendClient: friend.NewFriendServiceClient(conn)}
}

func (h *FriendHandler) SendRequest(c *gin.Context) {
	var req friend.SendRequestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.SendRequest(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) AcceptRequest(c *gin.Context) {
	req := &friend.AcceptRequestReq{RequestId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.AcceptRequest(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) RejectRequest(c *gin.Context) {
	req := &friend.RejectRequestReq{RequestId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.RejectRequest(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) CancelRequest(c *gin.Context) {
	req := &friend.CancelRequestReq{RequestId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.CancelRequest(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListPendingRequests(c *gin.Context) {
	req := &friend.ListPendingRequestsReq{}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListPendingRequests(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListSentRequests(c *gin.Context) {
	req := &friend.ListSentRequestsReq{}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListSentRequests(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListFriends(c *gin.Context) {
	var req friend.ListFriendsReq
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListFriends(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) DeleteFriend(c *gin.Context) {
	req := &friend.DeleteFriendReq{FriendId: parseInt64(c.Param("user_id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.DeleteFriend(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) SetRemark(c *gin.Context) {
	var req friend.SetRemarkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.FriendId = parseInt64(c.Param("user_id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.SetRemark(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) SetGroup(c *gin.Context) {
	var req friend.SetGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.FriendId = parseInt64(c.Param("user_id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.SetGroup(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) CreateGroup(c *gin.Context) {
	var req friend.CreateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.CreateGroup(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) RenameGroup(c *gin.Context) {
	var req friend.RenameGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.GroupId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.RenameGroup(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) DeleteGroup(c *gin.Context) {
	req := &friend.DeleteGroupReq{GroupId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.DeleteGroup(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListGroups(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListGroups(ctx, &friend.ListGroupsReq{})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) BlockUser(c *gin.Context) {
	req := &friend.BlockUserReq{BlockedUserId: parseInt64(c.Param("user_id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.BlockUser(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) UnblockUser(c *gin.Context) {
	req := &friend.UnblockUserReq{BlockedUserId: parseInt64(c.Param("user_id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.UnblockUser(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *FriendHandler) ListBlacklist(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.friendClient.ListBlacklist(ctx, &friend.ListBlacklistReq{UserId: c.GetInt64(middleware.CtxKeyUserID), Pagination: &common.Pagination{}})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}
