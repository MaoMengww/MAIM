package handler

import (
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
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
	// 会话类型沿用文本协议；实体引用采用规范 UUID 与显式 presence。
	var body struct {
		Type       string   `json:"type"`
		PeerUserID *string  `json:"peer_user_id"`
		MemberIDs  []string `json:"member_ids"`
		GroupName  string   `json:"group_name"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	req := message.CreateConversationReq{
		CreatorId: c.GetString(middleware.CtxKeyUserID),
	}
	switch body.Type {
	case "single":
		req.Type = message.ConversationType_CONVERSATION_TYPE_PRIVATE
		if body.PeerUserID == nil || identity.Validate(*body.PeerUserID) != nil {
			response.BadRequest(c, "invalid peer user identity")
			return
		}
		req.PeerUserId = body.PeerUserID
	case "group":
		req.Type = message.ConversationType_CONVERSATION_TYPE_GROUP
		for _, id := range body.MemberIDs {
			if err := identity.Validate(id); err != nil {
				response.BadRequest(c, "invalid member identity")
				return
			}
		}
		req.MemberIds = body.MemberIDs
		if body.GroupName != "" {
			req.Name = &body.GroupName
		}
	default:
		response.BadRequest(c, "invalid conversation type")
		return
	}

	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.CreateConversation(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Created(c, resp)
}

func (h *ConversationHandler) GetConversation(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.GetConversationReq{
		ConversationId: c.Param("id"),
		UserId:         c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.GetConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) DeleteConversation(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.DeleteConversationReq{
		ConversationId: c.Param("id"),
		UserId:         c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.DeleteConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateConversation(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.UpdateConversationReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.UpdateConversation(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UploadConvAvatar(c *gin.Context) {
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
	convID := c.Param("id")

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
		UserId: c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.ListConversations(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) GetMembers(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.GetMembersReq{ConversationId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.GetMembers(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) AddMembers(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.AddMembersReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.AddMembers(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) RemoveMembers(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.RemoveMembersReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.RemoveMembers(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateMember(c *gin.Context) {
	if !requirePathIdentities(c, "id", "uid") {
		return
	}
	var req message.UpdateMemberReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.UserId = c.Param("uid")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.UpdateMember(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MuteMember(c *gin.Context) {
	if !requirePathIdentities(c, "id", "uid") {
		return
	}
	var req message.MuteMemberReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.UserId = c.Param("uid")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.MuteMember(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UnmuteMember(c *gin.Context) {
	if !requirePathIdentities(c, "id", "uid") {
		return
	}
	req := &message.UnmuteMemberReq{
		ConversationId: c.Param("id"),
		UserId:         c.Param("uid"),
		OperatorId:     c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.UnmuteMember(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MuteAll(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.MuteAllReq{
		ConversationId: c.Param("id"),
		OperatorId:     c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.MuteAll(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UnmuteAll(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.UnmuteAllReq{
		ConversationId: c.Param("id"),
		OperatorId:     c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.UnmuteAll(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) SetAnnouncement(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.SetAnnouncementReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.SetAnnouncement(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) DeleteAnnouncement(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.DeleteAnnouncementReq{
		ConversationId: c.Param("id"),
		OperatorId:     c.GetString(middleware.CtxKeyUserID),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.DeleteAnnouncement(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) TransferOwner(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.TransferOwnerReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.OperatorId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.TransferOwner(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) UpdateSettings(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.UpdateSettingsReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.UpdateSettings(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) GetSettings(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &message.GetSettingsReq{ConversationId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.GetSettings(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) MarkAsRead(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req message.MarkAsReadReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.ConversationId = c.Param("id")
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.convClient.MarkAsRead(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationHandler) GetReadStatus(c *gin.Context) {
	if !requirePathIdentities(c, "id", "message_id") {
		return
	}
	req := &message.GetReadStatusReq{
		ConversationId: c.Param("id"),
		MessageId:      c.Param("message_id"),
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.convClient.GetReadStatus(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
