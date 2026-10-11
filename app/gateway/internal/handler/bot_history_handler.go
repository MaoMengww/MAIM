package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
)

type BotHistoryHandler struct {
	bots     botpb.BotServiceClient
	messages msgpb.MessageServiceClient
}

func NewBotHistoryHandler(bots botpb.BotServiceClient, messages msgpb.MessageServiceClient) *BotHistoryHandler {
	return &BotHistoryHandler{bots: bots, messages: messages}
}

func (h *BotHistoryHandler) Grant(c *gin.Context) {
	if !requirePathIdentities(c, "id", "bot_id") {
		return
	}
	var body struct {
		BindingID string `json:"binding_id" binding:"required"`
		StartSeq  int64  `json:"start_seq" binding:"required"`
		EndSeq    int64  `json:"end_seq" binding:"required"`
	}
	if c.ShouldBindJSON(&body) != nil || identity.Validate(body.BindingID) != nil || body.StartSeq < 1 || sequence.Validate(body.EndSeq) != nil || body.EndSeq < body.StartSeq {
		response.BadRequest(c, "invalid history range")
		return
	}
	req := &msgpb.GrantBotHistoryReq{ConversationId: c.Param("id"), BotId: c.Param("bot_id"), BindingId: body.BindingID, UserId: c.GetString(middleware.CtxKeyUserID), StartSeq: body.StartSeq, EndSeq: body.EndSeq}
	resp, err := h.messages.GrantBotHistory(middleware.WithGRPCMetadata(c), req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Created(c, resp)
}

func (h *BotHistoryHandler) authenticatedBot(c *gin.Context) string {
	parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		response.Unauthorized(c, "bot credential required")
		return ""
	}
	resp, err := h.bots.ValidateBotToken(c.Request.Context(), &botpb.ValidateBotTokenReq{Token: parts[1]})
	if err != nil || !resp.GetValid() || identity.Validate(resp.GetBotId()) != nil {
		response.Unauthorized(c, "invalid bot credential")
		return ""
	}
	return resp.GetBotId()
}

func (h *BotHistoryHandler) Read(c *gin.Context) {
	botID := h.authenticatedBot(c)
	if botID == "" {
		return
	}
	if !requirePathIdentities(c, "id") {
		return
	}
	bindingID, grantID := c.Query("binding_id"), c.Query("history_grant_id")
	if identity.Validate(bindingID) != nil || identity.Validate(grantID) != nil {
		response.BadRequest(c, "explicit binding and history grant required")
		return
	}
	var cursor int64
	limit := int64(30)
	var err error
	if value, exists := c.GetQuery("cursor"); exists {
		cursor, err = strconv.ParseInt(value, 10, 64)
		if err != nil || sequence.Validate(cursor) != nil {
			response.BadRequest(c, "invalid history cursor")
			return
		}
	}
	if value, exists := c.GetQuery("limit"); exists {
		limit, err = strconv.ParseInt(value, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			response.BadRequest(c, "invalid history limit")
			return
		}
	}
	req := &msgpb.GetMessagesReq{ConversationId: c.Param("id"), BotId: &botID, BindingId: &bindingID, HistoryGrantId: &grantID, Pagination: &msgpb.MessagePagination{Cursor: cursor, Limit: int32(limit)}}
	resp, err := h.messages.GetMessages(c.Request.Context(), req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ExecutorUpdate forwards the original update for runtime-owned validation.
func (h *BotHistoryHandler) ExecutorUpdate(c *gin.Context) {
	_, token, _ := strings.Cut(c.GetHeader("Authorization"), " ")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	raw, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "invalid executor update body")
		return
	}
	parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || token == "" {
		response.Unauthorized(c, "bot credential required")
		return
	}
	// Runtime authenticates the token and permits only narrow stop outcomes when
	// normal Bot eligibility has been revoked. No resource API uses this route.
	resp, err := h.bots.ExecutorUpdate(c.Request.Context(), &botpb.ExecutorUpdateReq{Token: token, Body: raw})
	if err != nil {
		runRPCError(c, err)
		return
	}
	writeRunJSON(c, resp.GetRunJson(), false)
}

func (h *BotHistoryHandler) Delegations(c *gin.Context) {
	botID := h.authenticatedBot(c)
	if botID == "" {
		return
	}
	_, token, _ := strings.Cut(c.GetHeader("Authorization"), " ")
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 131072)
	raw, err := c.GetRawData()
	if err != nil || !json.Valid(raw) {
		response.BadRequest(c, "invalid delegation body")
		return
	}
	result, err := h.bots.Authorization(c.Request.Context(), &botpb.AuthorizationReq{Kind: "delegation", ExecutorBotId: botID, Token: token, Body: raw})
	if err != nil {
		runRPCError(c, err)
		return
	}
	var payload any
	if json.Unmarshal(result.GetBody(), &payload) != nil {
		response.InternalError(c, "invalid delegation response")
		return
	}
	response.Success(c, payload)
}
