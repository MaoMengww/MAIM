package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/response"
	"github.com/maomeng/aim/pkg/identity"
)

func (h *BotSkillHandler) Candidates(c *gin.Context) {
	h.performWithBody(c, "candidate-list", []byte("{}"))
}

func (h *BotSkillHandler) ImportCandidateArchive(c *gin.Context) {
	h.perform(c, "candidate-import")
}

func (h *BotSkillHandler) Candidate(c *gin.Context) {
	if identity.Validate(c.Param("candidate")) != nil {
		response.BadRequest(c, "invalid candidate")
		return
	}
	h.performWithBody(c, "candidate-get", mustMarshal(map[string]string{"candidate_id": c.Param("candidate")}))
}

func (h *BotSkillHandler) ReviewCandidateCode(c *gin.Context) {
	h.candidate(c, "candidate-code-review")
}

func (h *BotSkillHandler) EvaluateCandidate(c *gin.Context) {
	h.candidate(c, "candidate-evaluate")
}

func (h *BotSkillHandler) RequestCandidateEvidence(c *gin.Context) {
	h.candidate(c, "candidate-request-evidence")
}

func (h *BotSkillHandler) RejectCandidate(c *gin.Context) {
	h.candidate(c, "candidate-reject")
}

func (h *BotSkillHandler) ApproveCandidate(c *gin.Context) {
	h.candidate(c, "candidate-approve")
}

func (h *BotSkillHandler) PublishCandidate(c *gin.Context) {
	h.candidate(c, "candidate-publish")
}

func (h *BotSkillHandler) ReviseCandidate(c *gin.Context) {
	h.candidate(c, "candidate-revise")
}

func (h *BotSkillHandler) ReintegrateCandidate(c *gin.Context) {
	h.candidate(c, "candidate-reintegrate")
}
