package handler

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	botpb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/app/gateway/internal/middleware"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/identity"
)

// BotSkillHandler forwards Bot-scoped skill governance to the Bot domain. The
// gateway only authenticates, bounds the body and preserves submission identity.
type BotSkillHandler struct {
	bots botpb.BotServiceClient
}

func NewBotSkillHandler(bots botpb.BotServiceClient) *BotSkillHandler {
	return &BotSkillHandler{bots: bots}
}

func (h *BotSkillHandler) perform(c *gin.Context, operation string) {
	c.Header("Cache-Control", "no-store")
	if !requirePathIdentities(c, "id") {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	raw, err := c.GetRawData()
	if err != nil || len(raw) > 0 && !json.Valid(raw) {
		response.BadRequest(c, "invalid skill operation body")
		return
	}
	req := &botpb.SkillOperationReq{BotId: c.Param("id"), Operation: operation, Body: raw}
	resp, err := h.bots.SkillOperation(middleware.WithGRPCMetadata(c), req)
	if err != nil {
		runRPCError(c, err)
		return
	}
	var payload any
	if json.Unmarshal(resp.GetBody(), &payload) != nil {
		response.InternalError(c, "invalid skill operation response")
		return
	}
	response.Success(c, payload)
}

func (h *BotSkillHandler) List(c *gin.Context) { h.perform(c, "list") }

func (h *BotSkillHandler) Available(c *gin.Context) { h.perform(c, "available") }

func (h *BotSkillHandler) Import(c *gin.Context) { h.perform(c, "import") }

func (h *BotSkillHandler) Get(c *gin.Context) {
	if err := identity.Validate(c.Param("candidate")); err != nil {
		response.BadRequest(c, "invalid candidate")
		return
	}
	h.performWithBody(c, "get", mustMarshal(map[string]string{"candidate_id": c.Param("candidate")}))
}

func mustMarshal(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}

// Candidate operations bind the path identity into the protocol body so the
// Bot domain never trusts a candidate ID that the client could repoint.
func (h *BotSkillHandler) candidate(c *gin.Context, operation string) {
	if err := identity.Validate(c.Param("candidate")); err != nil {
		response.BadRequest(c, "invalid candidate")
		return
	}
	body, err := mergePathBody(c, map[string]string{"candidate_id": c.Param("candidate")})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.Request.Body = http.NoBody
	c.Set("skill_body", body)
	h.performWithBody(c, operation, body)
}

func (h *BotSkillHandler) performWithBody(c *gin.Context, operation string, body []byte) {
	c.Header("Cache-Control", "no-store")
	if !requirePathIdentities(c, "id") {
		return
	}
	req := &botpb.SkillOperationReq{BotId: c.Param("id"), Operation: operation, Body: body}
	resp, err := h.bots.SkillOperation(middleware.WithGRPCMetadata(c), req)
	if err != nil {
		runRPCError(c, err)
		return
	}
	var payload any
	if json.Unmarshal(resp.GetBody(), &payload) != nil {
		response.InternalError(c, "invalid skill operation response")
		return
	}
	response.Success(c, payload)
}

func (h *BotSkillHandler) Evaluate(c *gin.Context) { h.candidate(c, "evaluate") }
func (h *BotSkillHandler) Approve(c *gin.Context)  { h.candidate(c, "approve") }
func (h *BotSkillHandler) Publish(c *gin.Context)  { h.candidate(c, "publish") }

func (h *BotSkillHandler) VersionAction(c *gin.Context) {
	if err := identity.Validate(c.Param("version")); err != nil {
		response.BadRequest(c, "invalid skill version")
		return
	}
	body, err := mergePathBody(c, map[string]string{"version_id": c.Param("version")})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	h.performWithBody(c, "version-"+c.Param("action"), body)
}

func (h *BotSkillHandler) Versions(c *gin.Context) { h.perform(c, "version-list") }

// mergePathBody reads a small JSON object and forces the path identity over any
// conflicting client-provided field.
func mergePathBody(c *gin.Context, forced map[string]string) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	raw, err := c.GetRawData()
	if err != nil {
		return nil, err
	}
	fields := make(map[string]json.RawMessage)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
	}
	for key, value := range forced {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		fields[key] = encoded
	}
	return json.Marshal(fields)
}
