package reranker

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
)

// The normal Jev context limits are state + all questions (64k), and state
// + each individual question (32k). Do not enable the long-context danger zone.
// With no fixed-version tokenizer in the RPC contract, one token per encoded
// UTF-8 byte deliberately overestimates text tokens, including Chinese and JSON
// escapes. A further reserve covers provider framing/model/tokenizer metadata.
const (
	judgmentRequestTokens  = 64000
	judgmentQuestionTokens = 32000
	judgmentTokenReserve   = 1024
)

type judgmentBatch struct {
	request *pb.JudgeReq
	indices map[string]int
}

func encodedTextBytes(text string) int {
	data, _ := json.Marshal(text) // Valid UTF-8 has already been checked.
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

// planJudgments packs candidates into as few shared-state requests as the
// normal Jev context budgets allow, preserving candidate order. It never
// truncates a query/passage, splits a passage, or accepts partial work: a
// candidate that cannot fit even one request fails the whole work item before
// any external dispatch.
func planJudgments(req *domain.RerankRequest) ([]judgmentBatch, error) {
	if !utf8.ValidString(req.Query) {
		return nil, errors.New(errors.CodeRPCError, "judgment query is not UTF-8")
	}
	for _, candidate := range req.Candidates {
		if !utf8.ValidString(candidate.Content) {
			return nil, errors.New(errors.CodeRPCError, "judgment passage is not UTF-8")
		}
	}
	query, _ := json.Marshal(struct {
		Query string `json:"query"`
	}{req.Query})
	prefix := strings.TrimSuffix(string(query), "}") + ",\"passages\":["
	suffix := "]}"
	baseStateBytes := encodedTextBytes(prefix) + encodedTextBytes(suffix)
	var batches []judgmentBatch
	var batch judgmentBatch
	var parts []string
	stateBytes, questionsBytes, largestQuestion := baseStateBytes, 0, 0
	flush := func() {
		if len(batch.indices) == 0 {
			return
		}
		batch.request.State = prefix + strings.Join(parts, ",") + suffix
		batches = append(batches, batch)
		batch = judgmentBatch{}
		parts = nil
		stateBytes, questionsBytes, largestQuestion = baseStateBytes, 0, 0
	}
	for index, candidate := range req.Candidates {
		name := fmt.Sprintf("%s_%06d", evidenceQuestionV1, index)
		question := evidenceQuestion()
		question.Instructions += " Assess only the passage whose candidate_id is " + name + "; other passages are not evidence for this question."
		passage, _ := json.Marshal(struct {
			ID      string `json:"candidate_id"`
			Passage string `json:"passage"`
		}{name, candidate.Content})
		part := string(passage)
		cost := questionBytes(name, question)
		extraState := encodedTextBytes(part)
		fits := func() bool {
			separator := 0
			if len(batch.indices) > 0 {
				separator = 1
			}
			state := stateBytes + extraState + separator
			return state+questionsBytes+cost+judgmentTokenReserve <= judgmentRequestTokens &&
				state+max(largestQuestion, cost)+judgmentTokenReserve <= judgmentQuestionTokens
		}
		if !fits() && len(batch.indices) > 0 {
			flush()
		}
		if !fits() {
			return nil, errors.New(errors.CodeRPCError, "judgment passage exceeds safe context budget")
		}
		if batch.request == nil {
			batch = judgmentBatch{request: &pb.JudgeReq{ModelId: req.ModelID, OwnerId: req.OwnerID, Questions: map[string]*pb.JudgeQuestion{}}, indices: map[string]int{}}
		}
		if len(parts) > 0 {
			stateBytes++
		}
		parts = append(parts, part)
		stateBytes += extraState
		questionsBytes += cost
		largestQuestion = max(largestQuestion, cost)
		batch.request.Questions[name] = question
		batch.indices[name] = index
	}
	flush()
	return batches, nil
}
