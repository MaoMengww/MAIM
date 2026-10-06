package handler

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type ModelHandler struct {
	cli llmgateway.LLMGatewayClient
}

func NewModelHandler(conn grpc.ClientConnInterface) *ModelHandler {
	return &ModelHandler{cli: llmgateway.NewLLMGatewayClient(conn)}
}

func writeProtoJSON(c *gin.Context, msg any) {
	if pm, ok := msg.(proto.Message); ok {
		data, err := protocol.Marshal(pm)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": json.RawMessage(data)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": msg})
}

func (h *ModelHandler) ListModels(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	ownerID := c.GetString(middleware.CtxKeyUserID)
	resp, err := h.cli.ListModels(ctx, &llmgateway.ListModelsReq{
		Capability: c.Query("capability"),
		OwnerId:    &ownerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) CreateModel(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	var req llmgateway.CreateModelReq
	if err := bindUserOwnedJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	ownerID := c.GetString(middleware.CtxKeyUserID)
	req.OwnerId = &ownerID
	req.OwnerType = "user"
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.cli.CreateModel(ctx, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) UpdateModel(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	var req llmgateway.UpdateModelReq
	if err := bindJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	req.ModelId = c.Param("id")
	if !requireRequestIdentities(c, &req) {
		return
	}
	if _, err := h.cli.UpdateModel(ctx, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, gin.H{"id": req.ModelId})
}

func (h *ModelHandler) DeleteModel(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.cli.DeleteModel(ctx, &llmgateway.DeleteModelReq{
		ModelId: c.Param("id"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) ListBillingRecords(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	page := parseInt64(c.DefaultQuery("page", "1"))
	pageSize := parseInt64(c.DefaultQuery("page_size", "20"))
	resp, err := h.cli.ListBillingRecords(ctx, &llmgateway.ListBillingRecordsReq{
		OwnerId:  c.GetString(middleware.CtxKeyUserID),
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) GetBillingStats(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	req := &llmgateway.BillingStatsReq{OwnerId: c.GetString(middleware.CtxKeyUserID)}
	if c.Request.URL.Query().Has("bot_id") {
		botID := c.Query("bot_id")
		if err := identity.Validate(botID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid bot identity"})
			return
		}
		req.BotId = &botID
	}
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.cli.GetBillingStats(ctx, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}
