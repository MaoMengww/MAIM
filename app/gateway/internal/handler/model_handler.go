package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
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
		marshaler := protojson.MarshalOptions{
			EmitUnpopulated: true,
			UseProtoNames:   true,
		}
		data, err := marshaler.Marshal(pm)
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
	resp, err := h.cli.ListModels(ctx, &llmgateway.ListModelsReq{
		Capability: c.Query("capability"),
		OwnerId:    c.GetInt64(middleware.CtxKeyUserID),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) CreateModel(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	body, _ := io.ReadAll(c.Request.Body)
	var req llmgateway.CreateModelReq
	if err := protojson.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	req.OwnerId = c.GetInt64(middleware.CtxKeyUserID)
	resp, err := h.cli.CreateModel(ctx, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}

func (h *ModelHandler) UpdateModel(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	body, _ := io.ReadAll(c.Request.Body)
	var req llmgateway.UpdateModelReq
	if err := protojson.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	req.ModelId = parseInt64(c.Param("id"))
	if _, err := h.cli.UpdateModel(ctx, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, gin.H{"id": req.ModelId})
}

func (h *ModelHandler) DeleteModel(c *gin.Context) {
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.cli.DeleteModel(ctx, &llmgateway.DeleteModelReq{
		ModelId: parseInt64(c.Param("id")),
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
		OwnerId:  c.GetInt64(middleware.CtxKeyUserID),
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
	resp, err := h.cli.GetBillingStats(ctx, &llmgateway.BillingStatsReq{
		OwnerId: c.GetInt64(middleware.CtxKeyUserID),
		BotId:   parseInt64(c.DefaultQuery("bot_id", "0")),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": err.Error()})
		return
	}
	writeProtoJSON(c, resp)
}
