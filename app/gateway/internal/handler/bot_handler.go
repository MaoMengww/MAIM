package handler

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	aibot "github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	"github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	convpb "github.com/maomeng/aim/app/conversation-service/pb/conversation"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/consts"
	common "github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

type BotHandler struct {
	botClient   botplatform.BotPlatformClient
	convClient  convpb.ConversationServiceClient
	aiBotClient aibot.AiBotServiceClient
	fileClient  filepb.FileServiceClient
}

func NewBotHandler(botConn, convConn, aiBotConn, fileConn grpc.ClientConnInterface) *BotHandler {
	return &BotHandler{
		botClient:   botplatform.NewBotPlatformClient(botConn),
		convClient:  convpb.NewConversationServiceClient(convConn),
		aiBotClient: aibot.NewAiBotServiceClient(aiBotConn),
		fileClient:  filepb.NewFileServiceClient(fileConn),
	}
}

func (h *BotHandler) CreateBot(c *gin.Context) {
	var req botplatform.CreateBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.OwnerId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.CreateBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateBot(c *gin.Context) {
	var req botplatform.UpdateBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.UpdateBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UploadBotAvatar(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		response.BadRequest(c, "read file failed")
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}

	userID := c.GetInt64(middleware.CtxKeyUserID)
	botID := parseInt64(c.Param("id"))

	req := &filepb.UploadAvatarReq{
		Data:     data,
		UserId:   userID,
		MimeType: mimeType,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.fileClient.UploadAvatar(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 同步更新 Bot 的 avatar 字段
	if resp.Url != "" {
		_, err = h.botClient.UpdateBot(ctx, &botplatform.UpdateBotReq{
			BotId:  botID,
			UserId: userID,
			Avatar: resp.Url,
		})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
	}

	response.Success(c, resp)
}

func (h *BotHandler) DeleteBot(c *gin.Context) {
	req := &botplatform.DeleteBotReq{
		BotId:  parseInt64(c.Param("id")),
		UserId: c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.DeleteBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) GetBot(c *gin.Context) {
	req := &botplatform.GetBotReq{BotId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.GetBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListBots(c *gin.Context) {
	req := &botplatform.ListBotsReq{
		OwnerId: c.GetInt64(middleware.CtxKeyUserID),
		Status:  c.Query("status"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.ListBots(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) RotateSecret(c *gin.Context) {
	req := &botplatform.RotateSecretReq{
		BotId:  parseInt64(c.Param("id")),
		UserId: c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.RotateSecret(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) Webhook(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "failed to read body")
		return
	}
	req := &botplatform.WebhookReq{
		Body:      body,
		Signature: c.GetHeader(consts.HeaderAIMSignature),
		Timestamp: parseInt64(c.GetHeader(consts.HeaderAIMTimestamp)),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.HandleIncomingWebhook(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) IssueToken(c *gin.Context) {
	var req botplatform.IssueBotTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.IssueBotToken(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ValidateToken(c *gin.Context) {
	var req botplatform.ValidateBotTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.ValidateBotToken(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== AI Bot Streaming Chat ==========

func (h *BotHandler) StreamChat(c *gin.Context) {
	var req aibot.StreamChatReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	stream, err := h.aiBotClient.StreamChat(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		data, _ := json.Marshal(resp)
		c.SSEvent("message", string(data))
		c.Writer.Flush()
	}
}

// Bot in Conversation

func (h *BotHandler) AddBotToConv(c *gin.Context) {
	raw, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	var req convpb.AddBotReq
	if err := protojson.Unmarshal(raw, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.AddBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) RemoveBotFromConv(c *gin.Context) {
	req := &convpb.RemoveBotReq{
		ConversationId: parseInt64(c.Param("id")),
		BotId:          parseInt64(c.Param("bot_id")),
		OperatorId:     c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.RemoveBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateBotInConv(c *gin.Context) {
	var req convpb.UpdateBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.BotId = parseInt64(c.Param("bot_id"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.UpdateBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListConvBots(c *gin.Context) {
	req := &convpb.ListBotsReq{
		ConversationId: parseInt64(c.Param("id")),
		UserId:         c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.ListBots(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Global MCP Server Management ==========

func (h *BotHandler) CreateMcpServer(c *gin.Context) {
	var req botplatform.CreateMcpServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.CreateMcpServer(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateMcpServer(c *gin.Context) {
	var req botplatform.UpdateMcpServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.Id = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.UpdateMcpServer(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) DeleteMcpServer(c *gin.Context) {
	req := &botplatform.DeleteMcpServerReq{
		Id:     parseInt64(c.Param("id")),
		UserId: c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.DeleteMcpServer(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) GetMcpServer(c *gin.Context) {
	req := &botplatform.GetMcpServerReq{
		Id:     parseInt64(c.Param("id")),
		UserId: c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.GetMcpServer(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListMcpServers(c *gin.Context) {
	req := &botplatform.ListMcpServersReq{
		UserId: c.GetInt64(middleware.CtxKeyUserID),
		Status: c.Query("status"),
		Pagination: &common.Pagination{
			Page:     int32(parseInt64(c.Query("page"))),
			PageSize: int32(parseInt64(c.Query("page_size"))),
		},
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.ListMcpServers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Bot MCP Assignment ==========

func (h *BotHandler) AssignMcpToBot(c *gin.Context) {
	var req botplatform.AssignMcpToBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.AssignMcpToBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UnassignMcpFromBot(c *gin.Context) {
	req := &botplatform.UnassignMcpFromBotReq{
		BotId:       parseInt64(c.Param("id")),
		UserId:      c.GetInt64(middleware.CtxKeyUserID),
		McpServerId: parseInt64(c.Param("mcp_id")),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.UnassignMcpFromBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListBotMcpServers(c *gin.Context) {
	req := &botplatform.ListBotMcpServersReq{BotId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.ListBotMcpServers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateBotMcpServer(c *gin.Context) {
	var req botplatform.UpdateBotMcpServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	req.McpServerId = parseInt64(c.Param("mcp_id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.UpdateBotMcpServer(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== MCP Tool Discovery ==========

func (h *BotHandler) DiscoverMcpTools(c *gin.Context) {
	req := &botplatform.DiscoverMcpToolsReq{
		McpServerId: parseInt64(c.Param("id")),
		UserId:      c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.DiscoverMcpTools(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListMcpTools(c *gin.Context) {
	req := &botplatform.ListMcpToolsReq{
		McpServerId: parseInt64(c.Param("id")),
		UserId:      c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.ListMcpTools(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
