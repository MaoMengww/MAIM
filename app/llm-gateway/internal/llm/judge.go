package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

// System One reports rounded distributions. Validate their meaning without
// normalizing or replacing the provider's reported values.
const judgeProbabilityTolerance = 1e-4

var errJudgeResponse = errors.New("invalid judgment provider response")

var judgeHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ValidateJudgeRequest validates the text judgment contract before admission.
// Model identity, ownership and capability are checked by the RPC handler.
func ValidateJudgeRequest(req *pb.JudgeReq) error {
	invalid := func() error { return &RequestError{Err: fmt.Errorf("invalid judgment questions")} }
	if req == nil || len(req.Questions) == 0 {
		return invalid()
	}
	for name, question := range req.Questions {
		if strings.TrimSpace(name) == "" || question == nil {
			return invalid()
		}
		switch kind := question.Kind.(type) {
		case *pb.JudgeQuestion_Noul:
			if kind == nil || kind.Noul == nil {
				return invalid()
			}
		case *pb.JudgeQuestion_Choice:
			if kind == nil || kind.Choice == nil || len(kind.Choice.Criteria) == 0 || len(kind.Choice.Criteria) > 255 {
				return invalid()
			}
			for choice := range kind.Choice.Criteria {
				if strings.TrimSpace(choice) == "" {
					return invalid()
				}
			}
		case *pb.JudgeQuestion_Score:
			if kind == nil || kind.Score == nil || len(kind.Score.Criteria) == 0 || len(kind.Score.Criteria) > 10 {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	return nil
}

// Judge invokes only the native TypeSafe System One API. A response returned
// together with an error contains usage facts, not a usable partial judgment.
func Judge(ctx context.Context, provider, apiKey, baseURL, model string, req *pb.JudgeReq) (*pb.JudgeResp, error) {
	if provider != "typesafe" {
		return nil, &RequestError{Err: fmt.Errorf("unsupported judgment provider")}
	}
	if err := ValidateJudgeRequest(req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" {
		return nil, &RequestError{Err: fmt.Errorf("judgment model required")}
	}
	endpoint, err := judgeEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	questions := make(map[string]any, len(req.Questions))
	for name, question := range req.Questions {
		wire := map[string]any{"instructions": question.Instructions}
		switch kind := question.Kind.(type) {
		case *pb.JudgeQuestion_Noul:
			wire["type"] = "noul"
			criteria := map[string]string{}
			if kind.Noul.FalseCriteria != nil {
				criteria["false"] = *kind.Noul.FalseCriteria
			}
			if kind.Noul.TrueCriteria != nil {
				criteria["true"] = *kind.Noul.TrueCriteria
			}
			if len(criteria) > 0 {
				wire["criteria"] = criteria
			}
		case *pb.JudgeQuestion_Choice:
			wire["type"], wire["criteria"] = "choice", kind.Choice.Criteria
		case *pb.JudgeQuestion_Score:
			wire["type"], wire["criteria"] = "score", kind.Score.Criteria
		}
		questions[name] = wire
	}
	body, err := json.Marshal(map[string]any{"model": model, "state": req.State, "questions": questions})
	if err != nil {
		return nil, fmt.Errorf("judgment request encoding failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("invalid judgment endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	response, err := judgeHTTPClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("judgment provider transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if readErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("judgment provider response read failed")
	}
	if len(data) > maxProviderResponseBytes {
		return nil, fmt.Errorf("judgment provider response too large")
	}
	out, decodeErr := decodeJudgeResponse(data, req)
	if ctx.Err() != nil {
		if out != nil {
			out.Answers = nil
		}
		return out, ctx.Err()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Error bodies may contain echoed materials or credentials. Only retain
		// independently validated usage/model fields and the HTTP status.
		if out != nil {
			out.Answers = nil
		}
		return out, &HTTPError{StatusCode: response.StatusCode}
	}
	return out, decodeErr
}

func judgeEndpoint(baseURL string) (string, error) {
	if baseURL == "" {
		baseURL = "https://api.typesafe.ai"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", &RequestError{Err: fmt.Errorf("invalid judgment base_url")}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	u.Path += "/systemone"
	u.RawPath = ""
	return u.String(), nil
}

func decodeJudgeResponse(data []byte, req *pb.JudgeReq) (*pb.JudgeResp, error) {
	invalid := errJudgeResponse
	root, err := judgeObject(data)
	if err != nil {
		return nil, invalid
	}
	out := &pb.JudgeResp{}
	// Read each usage dimension independently, even when another dimension or
	// the answers are malformed. Unknown usage never becomes an explicit zero.
	usageValid := true
	if raw, exists := root["usage"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		usage, err := judgeObject(raw)
		if err != nil {
			usageValid = false
		} else {
			known := &pb.JudgeUsage{}
			for name, target := range map[string]**int32{"input_tokens": &known.InputTokens, "output_tokens": &known.OutputTokens} {
				if raw, exists := usage[name]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					var count *int32
					if err := json.Unmarshal(raw, &count); err != nil || count == nil || *count < 0 {
						usageValid = false
					} else {
						*target = count
					}
				}
			}
			if known.InputTokens != nil || known.OutputTokens != nil {
				out.Usage = known
			}
		}
	}
	var model *string
	if err := json.Unmarshal(root["model"], &model); err == nil && model != nil && strings.TrimSpace(*model) != "" {
		out.Model = *model
	} else {
		return out, invalid
	}
	if !usageValid {
		return out, invalid
	}
	answers, err := judgeObject(root["answers"])
	if err != nil || len(answers) != len(req.Questions) {
		return out, invalid
	}
	validated := make(map[string]*pb.JudgeAnswer, len(answers))
	for name, question := range req.Questions {
		answer, err := decodeJudgeAnswer(answers[name], question)
		if err != nil {
			return out, invalid
		}
		validated[name] = answer
	}
	out.Answers = validated
	return out, nil
}

func decodeJudgeAnswer(raw json.RawMessage, question *pb.JudgeQuestion) (*pb.JudgeAnswer, error) {
	invalid := errJudgeResponse
	fields, err := judgeObject(raw)
	if err != nil {
		return nil, invalid
	}
	var kind string
	if err := json.Unmarshal(fields["type"], &kind); err != nil {
		return nil, invalid
	}
	switch requested := question.Kind.(type) {
	case *pb.JudgeQuestion_Noul:
		if kind != "noul" {
			return nil, invalid
		}
		probability, err := judgeNumber(fields["noul"])
		if err != nil || !judgeProbability(probability) {
			return nil, invalid
		}
		return &pb.JudgeAnswer{Kind: &pb.JudgeAnswer_Noul{Noul: &pb.NoulAnswer{Probability: probability}}}, nil
	case *pb.JudgeQuestion_Choice:
		if kind != "choice" {
			return nil, invalid
		}
		var choice *string
		if err := json.Unmarshal(fields["choice"], &choice); err != nil || choice == nil {
			return nil, invalid
		}
		if _, exists := requested.Choice.Criteria[*choice]; !exists {
			return nil, invalid
		}
		probabilities, err := judgeDistribution(fields["probabilities"], len(requested.Choice.Criteria))
		if err != nil {
			return nil, invalid
		}
		for option := range requested.Choice.Criteria {
			p, exists := probabilities[option]
			if !exists || p > probabilities[*choice]+judgeProbabilityTolerance {
				return nil, invalid
			}
		}
		confidence, err := judgeConfidence(fields["confidence"])
		if err != nil {
			return nil, invalid
		}
		return &pb.JudgeAnswer{Kind: &pb.JudgeAnswer_Choice{Choice: &pb.ChoiceAnswer{
			Choice: *choice, Probabilities: probabilities, Confidence: confidence, Criteria: maps.Clone(requested.Choice.Criteria),
		}}}, nil
	case *pb.JudgeQuestion_Score:
		if kind != "score" {
			return nil, invalid
		}
		score, err := judgeNumber(fields["score"])
		if err != nil || score < 0 || score > float64(len(requested.Score.Criteria)-1) {
			return nil, invalid
		}
		probabilities, err := judgeDistribution(fields["probabilities"], len(requested.Score.Criteria))
		if err != nil {
			return nil, invalid
		}
		legend, err := judgeObject(fields["legend"])
		if err != nil || len(legend) != len(requested.Score.Criteria) {
			return nil, invalid
		}
		levels := make(map[string]string, len(legend))
		var expected float64
		for index, criterion := range requested.Score.Criteria {
			key := strconv.Itoa(index)
			var description *string
			if err := json.Unmarshal(legend[key], &description); err != nil || description == nil || *description != criterion {
				return nil, invalid
			}
			probability, exists := probabilities[key]
			if !exists {
				return nil, invalid
			}
			expected += float64(index) * probability
			levels[key] = *description
		}
		if math.Abs(score-expected) > judgeProbabilityTolerance*float64(max(1, len(levels)-1)) {
			return nil, invalid
		}
		confidence, err := judgeConfidence(fields["confidence"])
		if err != nil {
			return nil, invalid
		}
		return &pb.JudgeAnswer{Kind: &pb.JudgeAnswer_Score{Score: &pb.ScoreAnswer{
			Score: score, Probabilities: probabilities, Legend: levels, Confidence: confidence,
		}}}, nil
	default:
		return nil, invalid
	}
}

func judgeConfidence(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value, err := judgeNumber(raw)
	if err != nil || !judgeProbability(value) {
		return nil, fmt.Errorf("invalid judgment confidence")
	}
	return &value, nil
}

func judgeDistribution(raw json.RawMessage, count int) (map[string]float64, error) {
	invalid := errJudgeResponse
	fields, err := judgeObject(raw)
	if err != nil || len(fields) != count {
		return nil, invalid
	}
	probabilities := make(map[string]float64, count)
	var sum float64
	for key, raw := range fields {
		probability, err := judgeNumber(raw)
		if err != nil || !judgeProbability(probability) {
			return nil, invalid
		}
		probabilities[key] = probability
		sum += probability
	}
	if math.Abs(sum-1) > judgeProbabilityTolerance {
		return nil, invalid
	}
	return probabilities, nil
}

func judgeProbability(value float64) bool { return value >= 0 && value <= 1 }

func judgeNumber(raw json.RawMessage) (float64, error) {
	var value *float64
	if err := json.Unmarshal(raw, &value); err != nil || value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, fmt.Errorf("invalid judgment number")
	}
	return *value, nil
}

// Reject duplicate object keys instead of letting encoding/json silently replace
// an answer, criterion or usage fact. Unknown fields remain forward compatible.
func judgeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	invalid := errJudgeResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalid
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, invalid
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalid
		}
		if _, exists := fields[key]; exists {
			return nil, invalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalid
		}
		fields[key] = value
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, invalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, invalid
	}
	return fields, nil
}
