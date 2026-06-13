package handler

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

type AuthHandler struct {
	userClient user.UserServiceClient
}

func NewAuthHandler(conn grpc.ClientConnInterface) *AuthHandler {
	return &AuthHandler{userClient: user.NewUserServiceClient(conn)}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req user.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.Register(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Created(c, resp)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req user.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.Login(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req user.LogoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.Logout(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req user.RefreshTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.RefreshToken(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// Serialize each proto field individually so the User field
	// (added to the Go struct, not in the raw descriptor) is included.
	data := map[string]any{}
	if resp.Tokens != nil {
		if raw, err := protojson.Marshal(resp.Tokens); err == nil {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				data["tokens"] = v
			}
		}
	}
	if resp.User != nil {
		if raw, err := protojson.Marshal(resp.User); err == nil {
			var v any
			if json.Unmarshal(raw, &v) == nil {
				data["user"] = v
			}
		}
	}

	c.JSON(200, response.Response{Code: 0, Message: "ok", Data: data})
}

func (h *AuthHandler) OAuthLogin(c *gin.Context) {
	provider := c.Param("provider")
	var req user.OAuthLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.Provider = provider
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.OAuthLogin(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *AuthHandler) GetSessions(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.GetSessions(ctx, &common.Empty{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *AuthHandler) RevokeSession(c *gin.Context) {
	req := &user.RevokeSessionReq{SessionId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.userClient.RevokeSession(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
