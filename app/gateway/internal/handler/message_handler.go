package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	msgclient "github.com/maomeng/aim/app/message-service/client/messageservice"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

type MessageHandler struct {
	msgClient  msgclient.MessageService
	fileClient filepb.FileServiceClient
}

func NewMessageHandler(msgCli zrpc.Client, fileConn grpc.ClientConnInterface) *MessageHandler {
	return &MessageHandler{
		msgClient:  msgclient.NewMessageService(msgCli),
		fileClient: filepb.NewFileServiceClient(fileConn),
	}
}

type sendMessageDTO struct {
	ConvID       json.Number    `json:"conversation_id"`
	Content      map[string]any `json:"content"`
	ClientMsgID  string         `json:"client_msg_id"`
	ReplyToMsgID json.Number    `json:"reply_to_msg_id"`
	UserID       int64          `json:"-"`
}

func (h *MessageHandler) SendMessage(c *gin.Context) {
	rawDTO, protoReq, err := h.parseSendMessageRequest(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.SendMessage(ctx, protoReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	userID, _ := c.Get(middleware.CtxKeyUserID)
	response.Created(c, map[string]any{
		"id":            strconv.FormatInt(resp.MessageId, 10),
		"message_id":    strconv.FormatInt(resp.MessageId, 10),
		"conv_id":       rawDTO.ConvID,
		"from_user_id":  strconv.FormatInt(userID.(int64), 10),
		"seq":           strconv.FormatInt(resp.Seq, 10),
		"type":          protoReq.Type,
		"created_at":    strconv.FormatInt(resp.CreatedAt, 10),
		"client_msg_id": rawDTO.ClientMsgID,
		"content":       rawDTO.Content,
	})
}

func (h *MessageHandler) SyncMessages(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxKeyUserID)
	uid, ok := userID.(int64)
	if !ok {
		response.BadRequest(c, "invalid user id")
		return
	}
	req := &msgclient.SyncMessagesReq{
		ConversationId: parseInt64(c.Param("id")),
		UserId:         uid,
	}
	if fromSeq := parseInt64(c.Query("from_seq")); fromSeq > 0 {
		req.FromSeq = fromSeq
	}
	if limit := int32(parseInt64(c.Query("limit"))); limit > 0 {
		req.Limit = limit
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.SyncMessages(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) GetMessageByID(c *gin.Context) {
	req := &msgclient.GetMessageByIDReq{MessageId: parseInt64(c.Param("id"))}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.GetMessageByID(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) RecallMessage(c *gin.Context) {
	var req msgclient.RecallMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.MessageId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
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
	var dto editMessageDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req := msgclient.EditMessageReq{
		MessageId: parseInt64(c.Param("id")),
		Text:      &msgpb.TextContent{Text: dto.Text},
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.EditMessage(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) DeleteMessage(c *gin.Context) {
	var req msgclient.DeleteMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.MessageId = parseInt64(c.Param("id"))
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.DeleteMessage(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) ReplyMessage(c *gin.Context) {
	rawDTO, protoReq, err := h.parseSendMessageRequest(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.SendMessage(ctx, protoReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Created(c, map[string]any{
		"id":            strconv.FormatInt(resp.MessageId, 10),
		"message_id":    strconv.FormatInt(resp.MessageId, 10),
		"conv_id":       rawDTO.ConvID,
		"from_user_id":  strconv.FormatInt(rawDTO.UserID, 10),
		"seq":           strconv.FormatInt(resp.Seq, 10),
		"type":          protoReq.Type,
		"created_at":    strconv.FormatInt(resp.CreatedAt, 10),
		"client_msg_id": rawDTO.ClientMsgID,
		"content":       rawDTO.Content,
	})
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
	uid, ok := userID.(int64)
	if !ok {
		return nil, nil, strconv.ErrSyntax
	}
	dto.UserID = uid

	convID, _ := dto.ConvID.Int64()
	req := &msgpb.SendMessageReq{
		ConversationId: convID,
		FromUserId:     uid,
		ClientMsgId:    dto.ClientMsgID,
	}

	if text, ok := dto.Content["text"].(string); ok && text != "" {
		mentions := parseMentions(dto.Content["mentions"])
		if len(mentions) == 0 {
			mentions = parseMentions(dto.Content["mention_user_ids"])
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
		f := files[0].(map[string]any)
		mime := toString(f["mime_type"])
		fileID := toInt64(f["file_id"])

		url := toString(f["url"])
		if url == "" && fileID > 0 {
			ctx := middleware.WithGRPCMetadata(c)
			urlResp, err := h.fileClient.GetDownloadURL(ctx, &filepb.GetDownloadURLReq{FileId: fileID, UserId: uid})
			if err == nil && urlResp.DownloadUrl != "" {
				url = urlResp.DownloadUrl
				f["url"] = url
			}
		}

		req.Type = msgTypeFromMime(mime)
		switch req.Type {
		case msgpb.MessageType_MESSAGE_TYPE_IMAGE:
			req.Content = &msgpb.SendMessageReq_Image{Image: &msgpb.ImageContent{FileId: fileID, Url: url, Size: toInt64(f["size"])}}
		case msgpb.MessageType_MESSAGE_TYPE_AUDIO:
			req.Content = &msgpb.SendMessageReq_Audio{Audio: &msgpb.AudioContent{FileId: fileID, Url: url, Duration: toInt32(f["duration"]), Size: toInt64(f["size"])}}
		default:
			req.Content = &msgpb.SendMessageReq_File{File: &msgpb.FileContent{FileId: fileID, Url: url, Name: toString(f["file_name"]), Size: toInt64(f["size"]), MimeType: mime}}
		}
	}

	if replyToID, err := dto.ReplyToMsgID.Int64(); err == nil && replyToID != 0 {
		req.ReplyToId = &replyToID
	}

	return &dto, (*msgclient.SendMessageReq)(req), nil
}

func msgTypeFromMime(mime string) msgpb.MessageType {
	if mime == "" {
		return msgpb.MessageType_MESSAGE_TYPE_FILE
	}
	if len(mime) >= 6 && mime[:6] == "image/" {
		return msgpb.MessageType_MESSAGE_TYPE_IMAGE
	}
	if len(mime) >= 6 && mime[:6] == "video/" {
		return msgpb.MessageType_MESSAGE_TYPE_VIDEO
	}
	if len(mime) >= 6 && mime[:6] == "audio/" {
		return msgpb.MessageType_MESSAGE_TYPE_AUDIO
	}
	return msgpb.MessageType_MESSAGE_TYPE_FILE
}

func parseMentions(v any) []int64 {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int64, 0, len(raw))
	for _, m := range raw {
		switch val := m.(type) {
		case string:
			out = append(out, parseInt64(val))
		case float64:
			out = append(out, int64(val))
		}
	}
	return out
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
	req.UserId = c.GetInt64(middleware.CtxKeyUserID)
	if req.UserId == 0 {
		response.BadRequest(c, "invalid user id")
		return
	}
	convIDStr := c.Query("conversation_id")
	if convIDStr == "" {
		convIDStr = c.Query("conv_id")
	}
	if convID := parseInt64(convIDStr); convID > 0 {
		req.ConversationId = &convID
	}
	if kw := c.Query("keyword"); kw != "" {
		req.Keyword = kw
	} else {
		req.Keyword = c.Query("q")
	}
	if senderID := parseInt64(c.Query("sender_id")); senderID > 0 {
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
	resp, err := h.msgClient.SearchMessages(ctx, &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *MessageHandler) GetAroundSeq(c *gin.Context) {
	seqStr := c.Param("seq")
	seq, _ := strconv.ParseInt(seqStr, 10, 64)
	req := &msgclient.GetAroundSeqReq{
		ConversationId: parseInt64(c.Param("id")),
		Seq:            seq,
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.msgClient.GetAroundSeq(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
