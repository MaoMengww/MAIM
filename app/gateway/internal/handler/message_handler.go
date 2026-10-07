package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	msgclient "github.com/maomeng/aim/app/message-service/client/messageservice"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/sequence"
	"github.com/zeromicro/go-zero/zrpc"
)

type MessageHandler struct {
	msgClient msgclient.MessageService
}

func NewMessageHandler(msgCli zrpc.Client) *MessageHandler {
	return &MessageHandler{
		msgClient: msgclient.NewMessageService(msgCli),
	}
}

type sendMessageDTO struct {
	ConvID       string         `json:"conversation_id"`
	Content      map[string]any `json:"content"`
	ClientMsgID  string         `json:"client_msg_id"`
	ReplyToMsgID *string        `json:"reply_to_msg_id"`
	UserID       string         `json:"-"`
}

func (h *MessageHandler) SendMessage(c *gin.Context) {
	rawDTO, protoReq, err := h.parseSendMessageRequest(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, protoReq) {
		return
	}
	resp, err := h.msgClient.SendMessage(ctx, protoReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	writeSendMessageAck(c, rawDTO, resp)
}

func writeSendMessageAck(c *gin.Context, dto *sendMessageDTO, resp *msgclient.SendMessageResp) {
	msg := resp.GetMessage()
	if msg == nil || identity.Validate(msg.MessageId) != nil {
		response.InternalError(c, "invalid message identity")
		return
	}
	if err := sequence.Validate(msg.Seq); err != nil {
		response.InternalError(c, "invalid message sequence")
		return
	}
	if msg.ConversationId != dto.ConvID || msg.GetFromUserId() != dto.UserID {
		response.InternalError(c, "invalid message owner")
		return
	}
	content := dto.Content
	if msg.Type != msgpb.MessageType_MESSAGE_TYPE_TEXT {
		metadata := resp.GetFileMetadata()
		if metadata == nil || identity.Validate(metadata.FileId) != nil {
			response.InternalError(c, "invalid attachment metadata")
			return
		}
		file := map[string]any{
			"file_id":       metadata.FileId,
			"file_name":     metadata.Name,
			"mime_type":     metadata.MimeType,
			"size":          metadata.Size,
			"ext":           metadata.Ext,
			"format":        metadata.Ext,
			"url":           "",
			"thumbnail_url": "",
			"duration":      int32(0),
			"width":         int32(0),
			"height":        int32(0),
		}
		var fileID string
		switch msg.Type {
		case msgpb.MessageType_MESSAGE_TYPE_IMAGE:
			image := msg.GetImage()
			fileID = image.GetFileId()
			file["width"], file["height"], file["format"] = image.GetWidth(), image.GetHeight(), image.GetFormat()
		case msgpb.MessageType_MESSAGE_TYPE_FILE:
			fileID = msg.GetFile().GetFileId()
		case msgpb.MessageType_MESSAGE_TYPE_VIDEO:
			video := msg.GetVideo()
			fileID = video.GetFileId()
			file["width"], file["height"], file["duration"] = video.GetWidth(), video.GetHeight(), video.GetDuration()
		case msgpb.MessageType_MESSAGE_TYPE_AUDIO:
			audio := msg.GetAudio()
			fileID = audio.GetFileId()
			file["duration"] = audio.GetDuration()
		}
		if fileID != metadata.FileId {
			response.InternalError(c, "invalid attachment content")
			return
		}
		content = map[string]any{"files": []any{file}}
	}
	response.Created(c, map[string]any{
		"id":              msg.MessageId,
		"message_id":      msg.MessageId,
		"conv_id":         msg.ConversationId,
		"from_user_id":    msg.GetFromUserId(),
		"seq":             msg.Seq,
		"type":            msg.Type,
		"created_at":      strconv.FormatInt(msg.CreatedAt, 10),
		"client_msg_id":   dto.ClientMsgID,
		"reply_to_msg_id": msg.ReplyToId,
		"content":         content,
	})
}

func (h *MessageHandler) SyncMessages(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxKeyUserID)
	uid, ok := userID.(string)
	if !ok || identity.Validate(uid) != nil {
		response.BadRequest(c, "invalid user id")
		return
	}
	req := &msgclient.SyncMessagesReq{UserId: uid}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.BadRequest(c, "invalid query")
		return
	}
	if query.Has("position") {
		position, err := strconv.ParseInt(query.Get("position"), 10, 64)
		if err != nil || sequence.Validate(position) != nil {
			response.BadRequest(c, "invalid position")
			return
		}
		req.Position = position
	}
	if query.Has("limit") {
		limit, err := strconv.ParseInt(query.Get("limit"), 10, 32)
		if err != nil || limit < 0 {
			response.BadRequest(c, "invalid limit")
			return
		}
		req.Limit = int32(limit)
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.msgClient.SyncMessages(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) GetMessages(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.BadRequest(c, "invalid query")
		return
	}
	pagination := &msgpb.MessagePagination{}
	if query.Has("cursor") {
		cursor, err := strconv.ParseInt(query.Get("cursor"), 10, 64)
		if err != nil || sequence.Validate(cursor) != nil {
			response.BadRequest(c, "invalid history cursor")
			return
		}
		pagination.Cursor = cursor
	}
	if query.Has("limit") {
		limit, err := strconv.ParseInt(query.Get("limit"), 10, 32)
		if err != nil || limit < 0 {
			response.BadRequest(c, "invalid limit")
			return
		}
		pagination.Limit = int32(limit)
	}
	req := &msgpb.GetMessagesReq{
		ConversationId: c.Param("id"),
		UserId:         c.GetString(middleware.CtxKeyUserID),
		Pagination:     pagination,
	}
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.msgClient.GetMessages(middleware.WithGRPCMetadata(c), req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) GetMessageByID(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &msgclient.GetMessageByIDReq{MessageId: c.Param("id")}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.msgClient.GetMessageByID(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) RecallMessage(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req msgclient.RecallMessageReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.MessageId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.msgClient.RecallMessage(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

type editMessageDTO struct {
	Text string `json:"text"`
}

func (h *MessageHandler) EditMessage(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var dto editMessageDTO
	if err := bindJSON(c, &dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req := msgclient.EditMessageReq{
		MessageId: c.Param("id"),
		UserId:    c.GetString(middleware.CtxKeyUserID),
		Text:      &msgpb.TextContent{Text: dto.Text},
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.msgClient.EditMessage(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) DeleteMessage(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	var req msgclient.DeleteMessageReq
	if err := bindJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.MessageId = c.Param("id")
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.msgClient.DeleteMessage(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) ReplyMessage(c *gin.Context) {
	h.SendMessage(c)
}

func (h *MessageHandler) parseSendMessageRequest(c *gin.Context) (*sendMessageDTO, *msgclient.SendMessageReq, error) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	var dto sendMessageDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, nil, err
	}

	userID, _ := c.Get(middleware.CtxKeyUserID)
	uid, ok := userID.(string)
	if !ok || identity.Validate(uid) != nil {
		return nil, nil, strconv.ErrSyntax
	}
	dto.UserID = uid

	if err := identity.Validate(dto.ConvID); err != nil {
		return nil, nil, err
	}
	if err := identity.ValidateSubmissionKey(dto.ClientMsgID); err != nil {
		return nil, nil, err
	}
	if dto.ReplyToMsgID != nil {
		if err := identity.Validate(*dto.ReplyToMsgID); err != nil {
			return nil, nil, err
		}
	}
	req := &msgpb.SendMessageReq{
		ConversationId: dto.ConvID,
		FromUserId:     uid,
		ClientMsgId:    dto.ClientMsgID,
	}

	if text, ok := dto.Content["text"].(string); ok && text != "" {
		mentions, err := parseMentions(dto.Content["mentions"])
		if err != nil {
			return nil, nil, err
		}
		if len(mentions) == 0 {
			mentions, err = parseMentions(dto.Content["mention_user_ids"])
			if err != nil {
				return nil, nil, err
			}
		}
		req.Content = &msgpb.SendMessageReq_Text{
			Text: &msgpb.TextContent{
				Text:           text,
				MentionUserIds: mentions,
				MentionAll:     toBool(dto.Content["mention_all"]),
			},
		}
		req.Type = msgpb.MessageType_MESSAGE_TYPE_TEXT
	} else if files, ok := dto.Content["files"].([]any); ok && len(files) > 0 {
		f, ok := files[0].(map[string]any)
		if !ok {
			return nil, nil, errors.New("invalid attachment")
		}
		fileID, ok := f["file_id"].(string)
		if !ok || identity.Validate(fileID) != nil {
			return nil, nil, errors.New("invalid file identity")
		}

		req.Content = &msgpb.SendMessageReq_Attachment{
			Attachment: &msgpb.AttachmentContent{FileId: fileID, Duration: toInt32(f["duration"])},
		}
	}

	req.ReplyToId = dto.ReplyToMsgID

	return &dto, (*msgclient.SendMessageReq)(req), nil
}

func parseMentions(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, errors.New("invalid mention identities")
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		id, ok := value.(string)
		if !ok || identity.Validate(id) != nil {
			return nil, errors.New("invalid mention identity")
		}
		out = append(out, id)
	}
	return out, nil
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt64(v any) int64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		return parseInt64(n)
	case int64:
		return n
	}
	return 0
}

func toInt32(v any) int32 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int32(n)
	case int64:
		return int32(n)
	}
	return 0
}

func toBool(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func (h *MessageHandler) SearchMessages(c *gin.Context) {
	var req msgclient.SearchMessagesReq
	req.UserId = c.GetString(middleware.CtxKeyUserID)
	if identity.Validate(req.UserId) != nil {
		response.BadRequest(c, "invalid user id")
		return
	}
	convIDStr := c.Query("conversation_id")
	if convIDStr == "" {
		convIDStr = c.Query("conv_id")
	}
	if c.Request.URL.Query().Has("conversation_id") || c.Request.URL.Query().Has("conv_id") {
		if err := identity.Validate(convIDStr); err != nil {
			response.BadRequest(c, "invalid conversation identity")
			return
		}
		req.ConversationId = &convIDStr
	}
	if kw := c.Query("keyword"); kw != "" {
		req.Keyword = kw
	} else {
		req.Keyword = c.Query("q")
	}
	if senderID := c.Query("sender_id"); c.Request.URL.Query().Has("sender_id") {
		if err := identity.Validate(senderID); err != nil {
			response.BadRequest(c, "invalid sender identity")
			return
		}
		req.SenderId = &senderID
	}
	if senderType := strings.TrimSpace(c.Query("sender_type")); senderType != "" {
		req.SenderType = senderType
	}
	if startTime := parseInt64(c.Query("start_time")); startTime > 0 {
		req.StartTime = &startTime
	}
	if endTime := parseInt64(c.Query("end_time")); endTime > 0 {
		req.EndTime = &endTime
	}
	msgTypeVals := c.QueryArray("message_types")
	if len(msgTypeVals) == 0 {
		msgTypeVals = c.QueryArray("msg_type")
	}
	if len(msgTypeVals) > 0 {
		for _, raw := range msgTypeVals {
			if n := parseInt64(raw); n > 0 {
				req.MessageTypes = append(req.MessageTypes, msgpb.MessageType(n))
			}
		}
	}
	page := int32(1)
	pageSize := int32(20)
	if raw := parseInt64(c.Query("page")); raw > 0 {
		page = int32(raw)
	}
	if raw := parseInt64(c.Query("page_size")); raw > 0 {
		pageSize = int32(raw)
	}
	req.Pagination = &common.Pagination{Page: page, PageSize: pageSize}

	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, &req) {
		return
	}
	resp, err := h.msgClient.SearchMessages(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) GetAroundSeq(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	seqStr := c.Param("seq")
	seq, err := strconv.ParseInt(seqStr, 10, 64)
	if err != nil || sequence.Validate(seq) != nil {
		response.BadRequest(c, "invalid sequence")
		return
	}
	req := &msgclient.GetAroundSeqReq{
		ConversationId: c.Param("id"),
		UserId:         c.GetString(middleware.CtxKeyUserID),
		Seq:            seq,
	}
	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.msgClient.GetAroundSeq(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
