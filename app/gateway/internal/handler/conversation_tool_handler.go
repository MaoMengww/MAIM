package handler

import (
	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/identity"
)

type ConversationToolHandler struct {
	botClient botpb.BotServiceClient
}

func NewConversationToolHandler(botClient botpb.BotServiceClient) *ConversationToolHandler {
	return &ConversationToolHandler{
		botClient: botClient,
	}
}

func (h *ConversationToolHandler) Summarize(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	convID := c.Param("id")
	userID := c.GetString(middleware.CtxKeyUserID)

	var body struct {
		LastMessageCount int32 `json:"last_message_count"`
		StartTime        int64 `json:"start_time"`
		EndTime          int64 `json:"end_time"`
		All              bool  `json:"all"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	req := &botpb.SummarizeReq{ConvId: convID, UserId: userID}
	if body.All {
		req.Range = &botpb.SummarizeReq_All{All: true}
	} else if body.StartTime > 0 || body.EndTime > 0 {
		req.Range = &botpb.SummarizeReq_TimeRange{
			TimeRange: &botpb.TimeRange{StartTime: body.StartTime, EndTime: body.EndTime},
		}
	} else {
		count := body.LastMessageCount
		if count <= 0 {
			count = 100
		}
		req.Range = &botpb.SummarizeReq_LastMessageCount{LastMessageCount: count}
	}

	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.SummarizeConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) GetSummaries(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	convID := c.Param("id")

	req := &botpb.GetConvSummariesReq{
		ConvId: convID,
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}

	ctx := middleware.WithGRPCMetadata(c)
	if !requireRequestIdentities(c, req) {
		return
	}
	resp, err := h.botClient.GetConvSummaries(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) CreateTodo(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	convID := c.Param("id")
	var body struct {
		SummaryID *string `json:"summary_id"`
		Content   string  `json:"content"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if body.SummaryID != nil && identity.Validate(*body.SummaryID) != nil {
		response.BadRequest(c, "invalid summary identity")
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.CreateTodo(ctx, &botpb.CreateTodoReq{
		ConvId:    convID,
		SummaryId: body.SummaryID,
		Content:   body.Content,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Created(c, resp)
}

func (h *ConversationToolHandler) UpdateTodo(c *gin.Context) {
	if !requirePathIdentities(c, "id", "todoId") {
		return
	}
	todoID := c.Param("todoId")
	var body struct {
		Content *string `json:"content"`
		Done    *bool   `json:"done"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.botClient.UpdateTodo(ctx, &botpb.UpdateTodoReq{
		TodoId:  todoID,
		ConvId:  c.Param("id"),
		Content: body.Content,
		Done:    body.Done,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *ConversationToolHandler) DeleteTodo(c *gin.Context) {
	if !requirePathIdentities(c, "id", "todoId") {
		return
	}
	todoID := c.Param("todoId")
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.botClient.DeleteTodo(ctx, &botpb.DeleteTodoReq{TodoId: todoID, ConvId: c.Param("id")})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *ConversationToolHandler) ReplyCandidates(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	convID := c.Param("id")
	userID := c.GetString(middleware.CtxKeyUserID)

	var body struct {
		ReplyToMsgID *string `json:"reply_to_msg_id"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if body.ReplyToMsgID != nil && identity.Validate(*body.ReplyToMsgID) != nil {
		response.BadRequest(c, "invalid reply identity")
		return
	}

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.GenerateReplyCandidates(ctx, &botpb.ReplyCandidatesReq{
		ConvId:       convID,
		UserId:       userID,
		ReplyToMsgId: body.ReplyToMsgID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) Translate(c *gin.Context) {
	if !requirePathIdentities(c, "id") {
		return
	}
	msgID := c.Param("id")
	var body struct {
		Text       string `json:"text"`
		TargetLang string `json:"target_lang"`
	}
	if err := bindJSON(c, &body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if body.TargetLang == "" {
		body.TargetLang = "zh-CN"
	}

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.TranslateMessage(ctx, &botpb.TranslateMessageReq{
		Text:       body.Text,
		TargetLang: body.TargetLang,
		MsgId:      &msgID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
