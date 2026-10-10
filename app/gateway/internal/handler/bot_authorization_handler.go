package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
)

func (h *BotHandler) ResourceAuthorization(c *gin.Context) { h.authorization(c, "resource", false) }
func (h *BotHandler) ActionAuthorization(c *gin.Context)   { h.authorization(c, "action", false) }
func (h *BotHandler) ExecutorResourceAuthorization(c *gin.Context) {
	h.authorization(c, "resource", true)
}
func (h *BotHandler) ExecutorActionAuthorization(c *gin.Context) { h.authorization(c, "action", true) }

func (h *BotHandler) authorization(c *gin.Context, kind string, executor bool) {
	c.Header("Cache-Control", "no-store")
	req := &botpb.AuthorizationReq{Kind: kind}
	ctx := middleware.WithGRPCMetadata(c)
	if executor {
		if !requirePathIdentities(c, "id") {
			return
		}
		scheme, token, ok := strings.Cut(c.GetHeader("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			response.Unauthorized(c, "bot credential required")
			return
		}
		authenticated, err := h.botClient.ValidateBotToken(c.Request.Context(), &botpb.ValidateBotTokenReq{Token: token})
		if err != nil || !authenticated.GetValid() || authenticated.GetBotId() != c.Param("id") || authenticated.GetType() != "third_party" {
			response.Unauthorized(c, "invalid executor credential")
			return
		}
		req.ExecutorBotId, req.Token = authenticated.GetBotId(), token
		ctx = c.Request.Context()
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	raw, err := c.GetRawData()
	if err != nil || !json.Valid(raw) {
		response.BadRequest(c, "invalid authorization body")
		return
	}
	req.Body = raw
	resp, err := h.botClient.Authorization(ctx, req)
	if err != nil {
		runRPCError(c, err)
		return
	}
	var payload any
	if json.Unmarshal(resp.GetBody(), &payload) != nil {
		response.InternalError(c, "invalid authorization response")
		return
	}
	response.Success(c, payload)
}
