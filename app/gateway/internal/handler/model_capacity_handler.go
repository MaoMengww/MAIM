package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

func (h *ModelHandler) ListConnections(c *gin.Context) {
	resp, err := h.cli.ListConnections(middleware.WithGRPCMetadata(c), &pb.ListConnectionsReq{Page: int32(parseInt64(c.DefaultQuery("page", "1"))), PageSize: int32(parseInt64(c.DefaultQuery("page_size", "100")))})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) CreateConnection(c *gin.Context) {
	var req pb.CreateConnectionReq
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid connection request"})
		return
	}
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.cli.CreateConnection(middleware.WithGRPCMetadata(c), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) UpdateConnection(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req pb.UpdateConnectionReq
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid connection request"})
		return
	}
	req.ConnectionId = c.Param("id")
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.cli.UpdateConnection(middleware.WithGRPCMetadata(c), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) DeleteConnection(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	resp, err := h.cli.DeleteConnection(middleware.WithGRPCMetadata(c), &pb.DeleteConnectionReq{ConnectionId: c.Param("id")})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) ListQuotaPools(c *gin.Context) {
	resp, err := h.cli.ListQuotaPools(middleware.WithGRPCMetadata(c), &pb.ListQuotaPoolsReq{Page: int32(parseInt64(c.DefaultQuery("page", "1"))), PageSize: int32(parseInt64(c.DefaultQuery("page_size", "100")))})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) CreateQuotaPool(c *gin.Context) {
	var req pb.CreateQuotaPoolReq
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid quota pool request"})
		return
	}
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.cli.CreateQuotaPool(middleware.WithGRPCMetadata(c), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) UpdateQuotaPool(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req pb.UpdateQuotaPoolReq
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid quota pool request"})
		return
	}
	req.QuotaPoolId = c.Param("id")
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.cli.UpdateQuotaPool(middleware.WithGRPCMetadata(c), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) DeleteQuotaPool(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	resp, err := h.cli.DeleteQuotaPool(middleware.WithGRPCMetadata(c), &pb.DeleteQuotaPoolReq{QuotaPoolId: c.Param("id")})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeProtoJSON(c, resp)
}
