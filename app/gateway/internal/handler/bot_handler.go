package handler

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/identity"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/protocol"
	"google.golang.org/grpc"
)

type BotHandler struct {
	botClient  botpb.BotServiceClient
	convClient message.MessageServiceClient
	fileClient filepb.FileServiceClient
}

func NewBotHandler(botClient botpb.BotServiceClient, msgConn, fileConn grpc.ClientConnInterface) *BotHandler {
	return &BotHandler{
		botClient:  botClient,
		convClient: message.NewMessageServiceClient(msgConn),
		fileClient: filepb.NewFileServiceClient(fileConn),
	}
}

func (h *BotHandler) CreateBot(c *gin.Context) {
	var req botpb.CreateBotReq
	if err := bindUserOwnedJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ownerID := c.GetString(middleware.CtxKeyUserID)
	req.OwnerId = &ownerID
	req.OwnerType = "user"
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.CreateBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateBot(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req botpb.UpdateBotReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.UpdateBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UploadBotAvatar(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
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

	userID := c.GetString(middleware.CtxKeyUserID)
	botID := c.Param("id")

	req := &filepb.UploadAvatarReq{
		Data:     data,
		UserId:   userID,
		MimeType: mimeType,
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.fileClient.UploadAvatar(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 同步更新 Bot 的 avatar 字段
	if resp.Url != "" {
		_, err = h.botClient.UpdateBot(ctx, &botpb.UpdateBotReq{
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
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.DeleteBotReq{
		BotId:  c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.DeleteBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) GetBot(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.GetBotReq{BotId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.GetBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListBots(c *gin.Context) {
	ownerID := c.GetString(middleware.CtxKeyUserID)
	req := &botpb.ListBotsReq{
		OwnerId:   &ownerID,
		OwnerType: "user",
		Status:    c.Query("status"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.ListBots(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) RotateSecret(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.RotateSecretReq{
		BotId:  c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
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
	var payload struct {
		Type           string  `json:"type"`
		ConversationID string  `json:"conversation_id"`
		ReplyToID      *string `json:"reply_to_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		response.BadRequest(c, "invalid webhook payload")
		return
	}
	if payload.Type == "message.send" && identity.Validate(payload.ConversationID) != nil {
		response.BadRequest(c, "invalid conversation identity")
		return
	}
	if payload.ReplyToID != nil && identity.Validate(*payload.ReplyToID) != nil {
		response.BadRequest(c, "invalid reply identity")
		return
	}
	req := &botpb.WebhookReq{
		Body:      body,
		Signature: c.GetHeader(consts.HeaderAIMSignature),
		Timestamp: parseInt64(c.GetHeader(consts.HeaderAIMTimestamp)),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.HandleIncomingWebhook(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) IssueToken(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req botpb.IssueBotTokenReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.IssueBotToken(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ValidateToken(c *gin.Context) {
	var req botpb.ValidateBotTokenReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.ValidateBotToken(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== AI Bot Streaming Chat ==========

func (h *BotHandler) StreamChat(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := botpb.StreamChatReq{BotId: c.Param("id"), ConvId: c.Query("conv_id"), Message: c.Query("message")}
	if req.Message == "" {
		response.BadRequest(c, "message is required")
		return
	}
	if replyID, supplied := c.GetQuery("reply_to_msg_id"); supplied {
		req.ReplyToMsgId = &replyID
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	stream, err := h.botClient.StreamChat(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	resp, err := stream.Recv()
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	for {
		data, err := protocol.Marshal(resp)
		if err != nil {
			return
		}
		c.SSEvent("message", string(data))
		c.Writer.Flush()
		resp, err = stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.SSEvent("error", "bot stream failed")
			c.Writer.Flush()
			return
		}
	}
}

// Bot in Conversation

func (h *BotHandler) AddBotToConv(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	var req message.AddBotReq
	if err := protocol.Unmarshal(raw, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.AddBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) RemoveBotFromConv(c *gin.Context) {
	if !requirePathIdentities(c, "id", "bot_id") {
		return
	}
	req := &message.RemoveBotReq{
		ConversationId: c.Param("id"),
		BotId:          c.Param("bot_id"),
		OperatorId:     c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.RemoveBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateBotInConv(c *gin.Context) {
	if !requirePathIdentities(c, "id", "bot_id") {
		return
	}
	var req message.UpdateBotReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.BotId = c.Param("bot_id")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.UpdateBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListConvBots(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.ListBotsReq{
		ConversationId: c.Param("id"),
		UserId:         c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.ListBots(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Global MCP Server Management ==========

func (h *BotHandler) CreateMcpServer(c *gin.Context) {
	var req botpb.CreateMcpServerReq
	if err := bindUserOwnedJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.CreateMcpServer(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateMcpServer(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req botpb.UpdateMcpServerReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.Id = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.UpdateMcpServer(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) DeleteMcpServer(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.DeleteMcpServerReq{
		Id:     c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.DeleteMcpServer(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) GetMcpServer(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.GetMcpServerReq{
		Id:     c.Param("id"),
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.GetMcpServer(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListMcpServers(c *gin.Context) {
	req := &botpb.ListMcpServersReq{
		UserId: c.GetString(middleware.CtxKeyUserID),
		Status: c.Query("status"),
		Pagination: &common.Pagination{
			Page:     int32(parseInt64(c.Query("page"))),
			PageSize: int32(parseInt64(c.Query("page_size"))),
		},
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.ListMcpServers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== Bot MCP Assignment ==========

func (h *BotHandler) AssignMcpToBot(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req botpb.AssignMcpToBotReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.AssignMcpToBot(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UnassignMcpFromBot(c *gin.Context) {
	if !requirePathIdentities(c, "id", "mcp_id") {
		return
	}
	req := &botpb.UnassignMcpFromBotReq{
		BotId:       c.Param("id"),
		UserId:      c.GetString(middleware.CtxKeyUserID),
		McpServerId: c.Param("mcp_id"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.UnassignMcpFromBot(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListBotMcpServers(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.ListBotMcpServersReq{BotId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.ListBotMcpServers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) UpdateBotMcpServer(c *gin.Context) {
	if !requirePathIdentities(c, "id", "mcp_id") {
		return
	}
	var req botpb.UpdateBotMcpServerReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.BotId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	req.McpServerId = c.Param("mcp_id")
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.botClient.UpdateBotMcpServer(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ========== MCP Tool Discovery ==========

func (h *BotHandler) DiscoverMcpTools(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.DiscoverMcpToolsReq{
		McpServerId: c.Param("id"),
		UserId:      c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.DiscoverMcpTools(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *BotHandler) ListMcpTools(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.ListMcpToolsReq{
		McpServerId: c.Param("id"),
		UserId:      c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.ListMcpTools(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
