package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"google.golang.org/grpc"
)

type ConversationToolHandler struct {
	aiBotClient aibot.ConversationToolServiceClient
}

func NewConversationToolHandler(conn grpc.ClientConnInterface) *ConversationToolHandler {
	return &ConversationToolHandler{
		aiBotClient: aibot.NewConversationToolServiceClient(conn),
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

	req := &aibot.SummarizeReq{ConvId: convID, UserId: userID}
	if body.All {
		req.Range = &aibot.SummarizeReq_All{All: true}
	} else if body.StartTime > 0 || body.EndTime > 0 {
		req.Range = &aibot.SummarizeReq_TimeRange{
			TimeRange: &aibot.TimeRange{StartTime: body.StartTime, EndTime: body.EndTime},
		}
	} else {
		count := body.LastMessageCount
		if count <= 0 {
			count = 100
		}
		req.Range = &aibot.SummarizeReq_LastMessageCount{LastMessageCount: count}
	}

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.aiBotClient.SummarizeConversation(ctx, req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

func (h *ConversationToolHandler) GetSummaries(c *gin.Context) {
	convID := parseInt64(c.Param("id"))
	userID := c.GetInt64(middleware.CtxKeyUserID)

	req := &aibot.GetConvSummariesReq{
		ConvId: convID,
		Limit:  int32(parseInt64(c.DefaultQuery("limit", "20"))),
	}
	_, _ = userID, req // userID available for future auth

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.aiBotClient.GetConvSummaries(ctx, req)
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
	resp, err := h.aiBotClient.CreateTodo(ctx, &aibot.CreateTodoReq{
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
	_, err := h.aiBotClient.UpdateTodo(ctx, &aibot.UpdateTodoReq{
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
	_, err := h.aiBotClient.DeleteTodo(ctx, &aibot.DeleteTodoReq{TodoId: todoID})
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
		ReplyToMsgID int64 `json:"reply_to_msg_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	ctx := middleware.WithGRPCMetadata(c)
	resp, err := h.aiBotClient.GenerateReplyCandidates(ctx, &aibot.ReplyCandidatesReq{
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
	resp, err := h.aiBotClient.TranslateMessage(ctx, &aibot.TranslateMessageReq{
		Text:       body.Text,
		TargetLang: body.TargetLang,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
