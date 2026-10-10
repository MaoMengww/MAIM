package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	common "github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	body, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "failed to read body")
		return
	}
	req := &botpb.WebhookReq{
		Body:      body,
		Signature: c.GetHeader(consts.HeaderAIMSignature),
		Timestamp: parseInt64(c.GetHeader(consts.HeaderAIMTimestamp)),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.HandleIncomingWebhook(ctx, req)
	if err != nil {
		runRPCError(c, err)
		return
	}
	writeRunJSON(c, resp.GetRunJson(), false)
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

// Run acceptance is independent of the lifetime of this HTTP request.
func (h *BotHandler) StartRun(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !requirePathIdentities(c, "id") {
		return
	}
	var req botpb.StartRunReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, "invalid run request")
		return
	}
	if req.BotId != "" && req.BotId != c.Param("id") {
		response.BadRequest(c, "bot identity does not match path")
		return
	}
	req.BotId = c.Param("id")
	if req.Visibility != "" && req.Visibility != "conversation" && req.Visibility != "personal" {
		response.BadRequest(c, "invalid run visibility")
		return
	}
	if !requireRequestIdentities(c, &req) {
		return
	}
	if len(req.MessageIds) > 100 || len(req.FileIds) > 100 || len(req.ResourceGrantIds) > 100 {
		response.BadRequest(c, "too many material references")
		return
	}
	if strings.TrimSpace(req.Message) == "" || len(req.Message) > 65536 {
		response.BadRequest(c, "message must contain between 1 and 65536 bytes")
		return
	}
	resp, err := h.botClient.StartRun(middleware.WithGRPCMetadata(c), &req)
	if err != nil {
		runRPCError(c, err)
		return
	}
	writeRunJSON(c, resp.GetRunJson(), false)
}

func (h *BotHandler) GetRun(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !requirePathIdentities(c, "id") {
		return
	}
	resp, err := h.botClient.GetRun(middleware.WithGRPCMetadata(c), &botpb.GetRunReq{RunId: c.Param("id")})
	if err != nil {
		runRPCError(c, err)
		return
	}
	writeRunJSON(c, resp.GetRunJson(), false)
}

func (h *BotHandler) ControlRun(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !requirePathIdentities(c, "id") {
		return
	}
	var req botpb.ControlRunReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, "invalid run control request")
		return
	}
	if req.RunId != "" && req.RunId != c.Param("id") {
		response.BadRequest(c, "run identity does not match path")
		return
	}
	req.RunId = c.Param("id")
	if !requireRequestIdentities(c, &req) {
		return
	}
	if len(req.MessageIds) > 100 || len(req.FileIds) > 100 {
		response.BadRequest(c, "too many material references")
		return
	}
	if len(req.Message) > 65536 {
		response.BadRequest(c, "message must not exceed 65536 bytes")
		return
	}
	resp, err := h.botClient.ControlRun(middleware.WithGRPCMetadata(c), &req)
	if err != nil {
		runRPCError(c, err)
		return
	}
	writeRunJSON(c, resp.GetRunJson(), false)
}

func (h *BotHandler) ListRuns(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !requirePathIdentities(c, "id") {
		return
	}
	limit := int64(50)
	if value, supplied := c.GetQuery("limit"); supplied {
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil || parsed < 1 || parsed > 100 {
			response.BadRequest(c, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	resp, err := h.botClient.ListRuns(middleware.WithGRPCMetadata(c), &botpb.ListRunsReq{ConvId: c.Param("id"), Limit: int32(limit)})
	if err != nil {
		runRPCError(c, err)
		return
	}
	writeRunJSON(c, resp.GetRunsJson(), true)
}

func writeRunJSON(c *gin.Context, value string, list bool) {
	raw := json.RawMessage(value)
	trimmed := strings.TrimSpace(value)
	if !json.Valid(raw) || trimmed == "" || (!list && trimmed[0] != '{') || (list && trimmed[0] != '[') {
		response.InternalError(c, "invalid run response contract")
		return
	}
	response.Success(c, raw)
}

func runRPCError(c *gin.Context, err error) {
	if status.Code(err) == codes.Aborted || status.Code(err) == codes.FailedPrecondition || status.Code(err) == codes.Unimplemented {
		response.Conflict(c, status.Convert(err).Message())
		return
	}
	response.GRPCError(c, err)
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
