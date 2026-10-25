package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/identity"
)

func (h *BotSkillHandler) ReviewPolicy(c *gin.Context) {
	h.performWithBody(c, "review-policy-get", mustMarshal(map[string]string{"conv_id": c.Query("conv_id"), "visibility": c.DefaultQuery("visibility", "conversation")}))
}
func (h *BotSkillHandler) SetReviewPolicy(c *gin.Context) { h.perform(c, "review-policy-set") }
func (h *BotSkillHandler) Reviews(c *gin.Context) {
	h.performWithBody(c, "review-list", mustMarshal(map[string]string{"conv_id": c.Query("conv_id"), "visibility": c.DefaultQuery("visibility", "conversation")}))
}
func (h *BotSkillHandler) Review(c *gin.Context) {
	if identity.Validate(c.Param("review")) != nil {
		response.BadRequest(c, "invalid review")
		return
	}
	h.performWithBody(c, "review-get", mustMarshal(map[string]string{"review_id": c.Param("review")}))
}
func (h *BotSkillHandler) CancelReview(c *gin.Context) {
	if identity.Validate(c.Param("review")) != nil {
		response.BadRequest(c, "invalid review")
		return
	}
	body, err := mergePathBody(c, map[string]string{"review_id": c.Param("review")})
	if err != nil {
		response.BadRequest(c, "invalid review request")
		return
	}
	h.performWithBody(c, "review-cancel", body)
}

func (h *BotSkillHandler) SubmitRunReview(c *gin.Context) {
	if identity.Validate(c.Param("run")) != nil {
		response.BadRequest(c, "invalid Run")
		return
	}
	body, err := mergePathBody(c, map[string]string{"run_id": c.Param("run")})
	if err != nil {
		response.BadRequest(c, "invalid review request")
		return
	}
	h.performWithBody(c, "review-submit", body)
}
