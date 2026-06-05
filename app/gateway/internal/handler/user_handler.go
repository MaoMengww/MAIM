package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
)

type UserHandler struct {
	userClient user.UserServiceClient
}

func NewUserHandler(conn grpc.ClientConnInterface) *UserHandler {
	return &UserHandler{userClient: user.NewUserServiceClient(conn)}
}

func (h *UserHandler) GetProfile(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.GetProfile(ctx, &common.Empty{})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) UpdateProfile(c *gin.Context) {
	var req user.UpdateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.UpdateProfile(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) UpdatePassword(c *gin.Context) {
	var req user.UpdatePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.UpdatePassword(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) BindPhone(c *gin.Context) {
	var req user.BindPhoneReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.BindPhone(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) BindEmail(c *gin.Context) {
	var req user.BindEmailReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.BindEmail(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) GetUserInfo(c *gin.Context) {
	req := &user.GetUserInfoReq{UserId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.GetUserInfo(ctx, req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) BatchGetUserInfo(c *gin.Context) {
	var req user.BatchGetUserInfoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.BatchGetUserInfo(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) SearchUsers(c *gin.Context) {
	var req user.SearchUsersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.SearchUsers(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) BatchGetStatus(c *gin.Context) {
	var req user.BatchGetStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.BatchGetStatus(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) GetSettings(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.GetSettings(ctx, &common.Empty{})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) UpdateSettings(c *gin.Context) {
	var req user.UpdateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.UpdateSettings(ctx, &req)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}

func (h *UserHandler) Recharge(c *gin.Context) {
	var req struct {
		Amount float64 `json:"amount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Amount <= 0 {
		response.BadRequest(c, "amount must be > 0")
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.Recharge(ctx, &user.RechargeReq{
		UserId: c.GetInt64(middleware.CtxKeyUserID),
		Amount: req.Amount,
	})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, resp)
}
