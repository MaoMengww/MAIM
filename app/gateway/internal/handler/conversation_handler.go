package handler

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"google.golang.org/grpc"
)

type ConversationHandler struct {
	convClient message.MessageServiceClient
	fileClient filepb.FileServiceClient
}

func NewConversationHandler(msgConn grpc.ClientConnInterface, fileConn grpc.ClientConnInterface) *ConversationHandler {
	return &ConversationHandler{
		convClient: message.NewMessageServiceClient(msgConn),
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

	req := message.CreateConversationReq{
		CreatorId: c.GetInt64(middleware.CtxKeyUserID),
	}
	switch body.Type {
	case "single":
		req.Type = message.ConversationType_CONVERSATION_TYPE_PRIVATE
		if body.PeerUserID != "" {
			v := parseInt64(string(body.PeerUserID))
			req.PeerUserId = &v
		}
	case "group":
		req.Type = message.ConversationType_CONVERSATION_TYPE_GROUP
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
	req := &message.GetConversationReq{
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
	req := &message.DeleteConversationReq{
		ConversationId: parseInt64(c.Param("id")),
		UserId:         c.GetInt64(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.DeleteConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateConversation(c *gin.Context) {
	var req message.UpdateConversationReq
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
		_, err = h.convClient.UpdateConversation(ctx, &message.UpdateConversationReq{
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
	req := message.ListConversationsReq{
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
	req := &message.GetMembersReq{ConversationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.GetMembers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) AddMembers(c *gin.Context) {
	var req message.AddMembersReq
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
	var req message.RemoveMembersReq
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
	var req message.UpdateMemberReq
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
	var req message.MuteMemberReq
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
	req := &message.UnmuteMemberReq{
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
	req := &message.MuteAllReq{
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
	req := &message.UnmuteAllReq{
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
	var req message.SetAnnouncementReq
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
	req := &message.DeleteAnnouncementReq{
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
	var req message.TransferOwnerReq
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
	var req message.UpdateSettingsReq
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
	req := &message.GetSettingsReq{ConversationId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.convClient.GetSettings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MarkAsRead(c *gin.Context) {
	var req message.MarkAsReadReq
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
	req := &message.GetReadStatusReq{
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
