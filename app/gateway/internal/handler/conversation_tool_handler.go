package handler

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
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
	convID := parseInt64(c.Param("id"))
	userID := c.GetInt64(middleware.CtxKeyUserID)

	var body struct {
		LastMessageCount int32 `json:"last_message_count"`
		StartTime        int64 `json:"start_time"`
		EndTime          int64 `json:"end_time"`
		All              bool  `json:"all"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
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
	resp, err := h.botClient.SummarizeConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) GetSummaries(c *gin.Context) {
	convID := parseInt64(c.Param("id"))
	userID := c.GetInt64(middleware.CtxKeyUserID)

	req := &botpb.GetConvSummariesReq{
		ConvId: convID,
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}
	_, _ = userID, req // userID available for future auth

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.GetConvSummaries(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) CreateTodo(c *gin.Context) {
	convID := parseInt64(c.Param("id"))
	var body struct {
		SummaryID int64  `json:"summary_id"`
		Content   string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
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
	todoID := parseInt64(c.Param("todoId"))
	var body struct {
		Content string `json:"content"`
		Done    bool   `json:"done"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.botClient.UpdateTodo(ctx, &botpb.UpdateTodoReq{
		TodoId:  todoID,
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
	todoID := parseInt64(c.Param("todoId"))
	ctx := middleware.WithGRPCMetadata(c)
	_, err := h.botClient.DeleteTodo(ctx, &botpb.DeleteTodoReq{TodoId: todoID})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

func (h *ConversationToolHandler) ReplyCandidates(c *gin.Context) {
	convID := parseInt64(c.Param("id"))
	userID := c.GetInt64(middleware.CtxKeyUserID)

	var body struct {
		ReplyToMsgID json.Number `json:"reply_to_msg_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	replyToMsgID, _ := body.ReplyToMsgID.Int64()

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.botClient.GenerateReplyCandidates(ctx, &botpb.ReplyCandidatesReq{
		ConvId:       convID,
		UserId:       userID,
		ReplyToMsgId: replyToMsgID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) Translate(c *gin.Context) {
	msgID := parseInt64(c.Param("id"))
	var body struct {
		Text       string `json:"text"`
		TargetLang string `json:"target_lang"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
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
		MsgId:      msgID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
