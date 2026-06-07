package handler

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	convclient "github.com/maomeng/aim/app/conversation-service/client/conversationservice"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

type ConversationHandler struct {
	convClient convclient.ConversationService
	fileClient filepb.FileServiceClient
}

func NewConversationHandler(cli zrpc.Client, fileConn grpc.ClientConnInterface) *ConversationHandler {
	return &ConversationHandler{
		convClient: convclient.NewConversationService(cli),
		fileClient: filepb.NewFileServiceClient(fileConn),
	}
}

func (h *ConversationHandler) CreateConversation(c *gin.Context) {
	// Custom binding: frontend sends type as string and IDs as strings for JS precision
	var body struct {
		Type       string        `json:"type"`
		PeerUserID json.Number   `json:"peer_user_id"`
		MemberIDs  []json.Number `json:"member_ids"`
		GroupName  string        `json:"group_name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	req := convclient.CreateConversationReq{
		CreatorId: c.GetInt64(middleware.CtxKeyUserID),
	}
	switch body.Type {
	case "single":
		req.Type = conversation.ConversationType_CONVERSATION_TYPE_PRIVATE
		if body.PeerUserID != "" {
			v := parseInt64(string(body.PeerUserID))
			req.PeerUserId = &v
		}
	case "group":
		req.Type = conversation.ConversationType_CONVERSATION_TYPE_GROUP
		for _, id := range body.MemberIDs {
			req.MemberIds = append(req.MemberIds, parseInt64(string(id)))
		}
		if body.GroupName != "" {
			req.Name = &body.GroupName
		}
	default:
		response.BadRequest(c, "invalid conversation type")
		return
	}

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.CreateConversation(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Created(c, resp)
}

func (h *ConversationHandler) GetConversation(c *gin.Context) {
	req := &convclient.GetConversationReq{
		ConversationId: parseInt64(c.Param("id")),
		UserId:         c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.GetConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) DeleteConversation(c *gin.Context) {
	req := &convclient.DeleteConversationReq{ConversationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.DeleteConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateConversation(c *gin.Context) {
	var req convclient.UpdateConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.UpdateConversation(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UploadConvAvatar(c *gin.Context) {
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
	convID := parseInt64(c.Param("id"))

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

	// Update conversation avatar with the uploaded file URL
	if resp.Url != "" {
		avatar := resp.Url
		_, err = h.convClient.UpdateConversation(ctx, &conversation.UpdateConversationReq{
			ConversationId: convID,
			UserId:         userID,
			Avatar:         &avatar,
		})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
	}

	response.Success(c, resp)
}

func (h *ConversationHandler) ListConversations(c *gin.Context) {
	req := convclient.ListConversationsReq{
		UserId: c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.ListConversations(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) GetMembers(c *gin.Context) {
	req := &convclient.GetMembersReq{ConversationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.GetMembers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) AddMembers(c *gin.Context) {
	var req convclient.AddMembersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.AddMembers(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) RemoveMembers(c *gin.Context) {
	var req convclient.RemoveMembersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.RemoveMembers(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateMember(c *gin.Context) {
	var req convclient.UpdateMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.UserId = parseInt64(c.Param("uid"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.UpdateMember(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MuteMember(c *gin.Context) {
	var req convclient.MuteMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.UserId = parseInt64(c.Param("uid"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.MuteMember(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UnmuteMember(c *gin.Context) {
	req := &convclient.UnmuteMemberReq{
		ConversationId: parseInt64(c.Param("id")),
		UserId:         parseInt64(c.Param("uid")),
		OperatorId:     c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.UnmuteMember(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MuteAll(c *gin.Context) {
	req := &convclient.MuteAllReq{
		ConversationId: parseInt64(c.Param("id")),
		OperatorId:     c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.MuteAll(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UnmuteAll(c *gin.Context) {
	req := &convclient.UnmuteAllReq{
		ConversationId: parseInt64(c.Param("id")),
		OperatorId:     c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.UnmuteAll(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) SetAnnouncement(c *gin.Context) {
	var req convclient.SetAnnouncementReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.SetAnnouncement(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) DeleteAnnouncement(c *gin.Context) {
	req := &convclient.DeleteAnnouncementReq{
		ConversationId: parseInt64(c.Param("id")),
		OperatorId:     c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.DeleteAnnouncement(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) TransferOwner(c *gin.Context) {
	var req convclient.TransferOwnerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	req.OperatorId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.TransferOwner(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateSettings(c *gin.Context) {
	var req convclient.UpdateSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.UpdateSettings(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) GetSettings(c *gin.Context) {
	req := &convclient.GetSettingsReq{ConversationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.GetSettings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MarkAsRead(c *gin.Context) {
	var req convclient.MarkAsReadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = parseInt64(c.Param("id"))
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.MarkAsRead(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) GetReadStatus(c *gin.Context) {
	req := &convclient.GetReadStatusReq{
		ConversationId: parseInt64(c.Param("id")),
		MessageId:      parseInt64(c.Param("message_id")),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.GetReadStatus(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// Bot stubs - to be fully connected to bot-platform gRPC
func (h *ConversationHandler) CreateBot(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) ListBots(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) GetBot(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) UpdateBot(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) DeleteBot(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) RotateSecret(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) AddBot(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) RemoveBot(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) UpdateBotInConv(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) ListConvBots(c *gin.Context) {
	response.NotImplemented(c)
}

func (h *ConversationHandler) Webhook(c *gin.Context) {
	response.NotImplemented(c)
}
