package reranker

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
)

// Conservatively count encoded UTF-8 bytes as tokens, with framing reserve.
// Never truncate evidence or enable the provider's long-context danger zone.
const (
	judgmentQuestionTokens = 32000
	judgmentTokenReserve   = 1024
)

type judgmentBatch struct {
	request *pb.JudgeReq
	indices map[string]int
}

func encodedTextBytes(text string) int {
	data, _ := json.Marshal(text)
	return len(data) - 2
}

func questionBytes(name string, question *pb.JudgeQuestion) int {
	noul := question.GetNoul()
	wire := struct {
		Type         string            `json:"type"`
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	}{"noul", question.Instructions, map[string]string{"false": noul.GetFalseCriteria(), "true": noul.GetTrueCriteria()}}
	data, _ := json.Marshal(wire)
	return len(data) + encodedTextBytes(name) + 4
}

// planJudgments validates every candidate before dispatch. Each candidate has
// one independent request; no passage is split and no partial ranking is valid.
func planJudgments(req *domain.RerankRequest) ([]judgmentBatch, error) {
	if !utf8.ValidString(req.Query) {
		return nil, errors.New(errors.CodeRPCError, "judgment query is not UTF-8")
	}
	question := evidenceQuestion()
	cost := questionBytes(evidenceQuestionV1, question) + judgmentTokenReserve
	batches := make([]judgmentBatch, len(req.Candidates))
	for index, candidate := range req.Candidates {
		if !utf8.ValidString(candidate.Content) {
			return nil, errors.New(errors.CodeRPCError, "judgment passage is not UTF-8")
		}
		state := "Query:\n" + req.Query + "\n\nPassage:\n" + candidate.Content
		if encodedTextBytes(state)+cost > judgmentQuestionTokens {
			return nil, errors.New(errors.CodeRPCError, "judgment passage exceeds safe context budget")
		}
		batches[index] = judgmentBatch{
			request: &pb.JudgeReq{ModelId: req.ModelID, OwnerId: req.OwnerID, State: state, Questions: map[string]*pb.JudgeQuestion{evidenceQuestionV1: question}},
			indices: map[string]int{evidenceQuestionV1: index},
		}
	}
	return batches, nil
}
