package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

func judgeBoundaryRequest() *pb.JudgeReq {
	return &pb.JudgeReq{State: "private customer material", Questions: map[string]*pb.JudgeQuestion{
		"billing": {Instructions: "Is this billing?", Kind: &pb.JudgeQuestion_Noul{Noul: &pb.NoulQuestion{}}},
		"tone":    {Instructions: "Select tone", Kind: &pb.JudgeQuestion_Choice{Choice: &pb.ChoiceQuestion{Criteria: map[string]string{"calm": "Polite", "angry": "Hostile"}}}},
		"urgency": {Instructions: "Rate urgency", Kind: &pb.JudgeQuestion_Score{Score: &pb.ScoreQuestion{Criteria: []string{"Can wait", "Today", "Now"}}}},
	}}
}

const judgeBoundaryAnswers = `"answers":{"urgency":{"type":"score","score":1.7,"confidence":0.55,"legend":{"0":"Can wait","1":"Today","2":"Now"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}},"tone":{"type":"choice","choice":"calm","confidence":0.7,"probabilities":{"angry":0.2,"calm":0.8}},"billing":{"type":"noul","noul":0.98}}`

func TestJudgeSystemOneBoundary(t *testing.T) {
	request := judgeBoundaryRequest()
	falseCriterion, trueCriterion := "Not billing", "Billing"
	request.Questions["billing"].GetNoul().FalseCriteria = &falseCriterion
	request.Questions["billing"].GetNoul().TrueCriteria = &trueCriterion
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/native/v1/systemone" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect native System One request")
		}
		var body struct {
			Model     string `json:"model"`
			State     string `json:"state"`
			Questions map[string]struct {
				Type         string          `json:"type"`
				Instructions string          `json:"instructions"`
				Criteria     json.RawMessage `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("invalid System One JSON request")
		}
		if body.Model != "jev-requested-version" || body.State != request.State || len(body.Questions) != 3 || body.Questions["billing"].Type != "noul" || body.Questions["tone"].Type != "choice" || body.Questions["urgency"].Type != "score" {
			t.Error("judgment identity or typed questions not preserved")
		}
		var noul map[string]string
		if err := json.Unmarshal(body.Questions["billing"].Criteria, &noul); err != nil || noul["false"] != "Not billing" || noul["true"] != "Billing" {
			t.Error("Noul criteria not preserved")
		}
		_, _ = fmt.Fprint(w, `{"model":"jev-actual-version",`+judgeBoundaryAnswers+`,"usage":{"input_tokens":13,"output_tokens":7}}`)
	}))
	defer server.Close()
	response, err := Judge(t.Context(), "typesafe", "private-key", server.URL+"/native", "jev-requested-version", request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "jev-actual-version" || response.Usage == nil || response.Usage.InputTokens == nil || *response.Usage.InputTokens != 13 || response.Usage.OutputTokens == nil || *response.Usage.OutputTokens != 7 {
		t.Fatal("actual model or independent token usage was lost")
	}
	if response.Answers["billing"].GetNoul().Probability != 0.98 {
		t.Fatal("Noul probability or named association was lost")
	}
	choice := response.Answers["tone"].GetChoice()
	if choice == nil || choice.Choice != "calm" || choice.Probabilities["angry"] != 0.2 || choice.Confidence == nil || *choice.Confidence != 0.7 || choice.Criteria["calm"] != "Polite" {
		t.Fatal("Choice semantics were lost")
	}
	score := response.Answers["urgency"].GetScore()
	if score == nil || score.Score != 1.7 || score.Legend["2"] != "Now" || score.Probabilities["2"] != 0.8 || score.Confidence == nil || *score.Confidence != 0.55 {
		t.Fatal("Score semantics were lost")
	}
}

func TestJudgeRejectsMalformedAnswersAndPreservesUsage(t *testing.T) {
	for _, tc := range []struct{ name, answers string }{
		{"missing answer", `"answers":{}`},
		{"wrong association", strings.Replace(judgeBoundaryAnswers, `"billing":`, `"unrequested":`, 1)},
		{"wrong type", strings.Replace(judgeBoundaryAnswers, `"type":"noul"`, `"type":"box"`, 1)},
		{"missing probability", strings.Replace(judgeBoundaryAnswers, `,"noul":0.98`, ``, 1)},
		{"null probability", strings.Replace(judgeBoundaryAnswers, `"noul":0.98`, `"noul":null`, 1)},
		{"string probability", strings.Replace(judgeBoundaryAnswers, `"noul":0.98`, `"noul":"0.98"`, 1)},
		{"out of range probability", strings.Replace(judgeBoundaryAnswers, `"noul":0.98`, `"noul":1.01`, 1)},
		{"nonfinite probability", strings.Replace(judgeBoundaryAnswers, `"noul":0.98`, `"noul":1e400`, 1)},
		{"duplicate probability", strings.Replace(judgeBoundaryAnswers, `"noul":0.98`, `"noul":0.1,"noul":0.98`, 1)},
		{"duplicate answer", strings.Replace(judgeBoundaryAnswers, `"billing":`, `"billing":{"type":"noul","noul":0.1},"billing":`, 1)},
		{"unknown choice", strings.Replace(judgeBoundaryAnswers, `"choice":"calm"`, `"choice":"other"`, 1)},
		{"choice is not maximum", strings.Replace(judgeBoundaryAnswers, `"choice":"calm"`, `"choice":"angry"`, 1)},
		{"choice distribution keys", strings.Replace(judgeBoundaryAnswers, `"angry":0.2`, `"other":0.2`, 1)},
		{"distribution not normalized", strings.Replace(judgeBoundaryAnswers, `"angry":0.2`, `"angry":0.1`, 1)},
		{"null distribution value", strings.Replace(judgeBoundaryAnswers, `"angry":0.2`, `"angry":null`, 1)},
		{"negative confidence", strings.Replace(judgeBoundaryAnswers, `"confidence":0.7`, `"confidence":-0.1`, 1)},
		{"string confidence", strings.Replace(judgeBoundaryAnswers, `"confidence":0.7`, `"confidence":"0.7"`, 1)},
		{"score outside rubric", strings.Replace(judgeBoundaryAnswers, `"score":1.7`, `"score":2.1`, 1)},
		{"score not expectation", strings.Replace(judgeBoundaryAnswers, `"score":1.7`, `"score":1.1`, 1)},
		{"different legend", strings.Replace(judgeBoundaryAnswers, `"2":"Now"`, `"2":"Never"`, 1)},
		{"noncanonical score key", strings.Replace(judgeBoundaryAnswers, `"2":0.8`, `"02":0.8`, 1)},
		{"missing legend", strings.Replace(judgeBoundaryAnswers, `"legend":{"0":"Can wait","1":"Today","2":"Now"},`, ``, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"model":"actual",`+tc.answers+`,"usage":{"input_tokens":13,"output_tokens":7}}`)
			}))
			defer server.Close()
			response, err := Judge(t.Context(), "typesafe", "private-key", server.URL+"/v1", "requested", judgeBoundaryRequest())
			if err == nil || response == nil || response.Usage == nil || response.Usage.GetInputTokens() != 13 || response.Usage.GetOutputTokens() != 7 || len(response.Answers) != 0 {
				t.Fatal("malformed answers must fail without discarding actual usage or returning partial answers")
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("provider failure leaked sensitive content")
			}
		})
	}
}

func TestJudgeUsagePresenceAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, usage   string
		input, output bool
		invalid       bool
	}{
		{name: "missing"},
		{name: "null", usage: `,"usage":null`},
		{name: "empty", usage: `,"usage":{}`},
		{name: "input only", usage: `,"usage":{"input_tokens":13}`, input: true},
		{name: "output only", usage: `,"usage":{"output_tokens":7}`, output: true},
		{name: "known zero", usage: `,"usage":{"input_tokens":0,"output_tokens":0}`, input: true, output: true},
		{name: "negative", usage: `,"usage":{"input_tokens":-1,"output_tokens":7}`, output: true, invalid: true},
		{name: "overflow", usage: `,"usage":{"input_tokens":2147483648,"output_tokens":7}`, output: true, invalid: true},
		{name: "fractional", usage: `,"usage":{"input_tokens":1.3,"output_tokens":7}`, output: true, invalid: true},
		{name: "string", usage: `,"usage":{"input_tokens":"13","output_tokens":7}`, output: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"model":"actual",`+judgeBoundaryAnswers+tc.usage+`}`)
			}))
			defer server.Close()
			response, err := Judge(t.Context(), "typesafe", "key", server.URL, "requested", judgeBoundaryRequest())
			if (err != nil) != tc.invalid || response == nil {
				t.Fatalf("unexpected judgment result: %v", err)
			}
			if (response.Usage != nil && response.Usage.InputTokens != nil) != tc.input || (response.Usage != nil && response.Usage.OutputTokens != nil) != tc.output {
				t.Fatal("unknown and reported-zero usage must remain distinct")
			}
		})
	}
}

func TestJudgeOptionalConfidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		answers := strings.Replace(judgeBoundaryAnswers, `"confidence":0.7,`, ``, 1)
		answers = strings.Replace(answers, `"confidence":0.55`, `"confidence":null`, 1)
		_, _ = fmt.Fprint(w, `{"model":"actual",`+answers+`}`)
	}))
	defer server.Close()
	response, err := Judge(t.Context(), "typesafe", "key", server.URL, "requested", judgeBoundaryRequest())
	if err != nil || response.Answers["tone"].GetChoice().Confidence != nil || response.Answers["urgency"].GetScore().Confidence != nil {
		t.Fatal("unreported confidence must remain absent")
	}
}

func TestJudgeRejectsUnsupportedRequestsBeforeHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid request reached provider")
	}))
	defer server.Close()
	for _, provider := range []string{"", "openai", "cohere"} {
		if _, err := Judge(t.Context(), provider, "key", server.URL, "model", judgeBoundaryRequest()); err == nil {
			t.Fatal("unsupported provider accepted")
		}
	}
	for _, request := range []*pb.JudgeReq{nil, {}, {Questions: map[string]*pb.JudgeQuestion{"q": {}}}, {Questions: map[string]*pb.JudgeQuestion{"q": {Kind: &pb.JudgeQuestion_Score{Score: &pb.ScoreQuestion{}}}}}} {
		if _, err := Judge(t.Context(), "typesafe", "key", server.URL, "model", request); err == nil {
			t.Fatal("invalid question accepted")
		}
	}
}

func TestJudgeHTTPFailureIsSafeAndRetainsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"detail":"private-key private customer material","usage":{"input_tokens":13,"output_tokens":7}}`)
	}))
	defer server.Close()
	response, err := Judge(t.Context(), "typesafe", "private-key", server.URL, "model", judgeBoundaryRequest())
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != http.StatusUnprocessableEntity || strings.Contains(err.Error(), "private") || response == nil || response.Usage == nil || response.Usage.GetOutputTokens() != 7 || len(response.Answers) != 0 {
		t.Fatal("HTTP rejection must be safe and preserve actual usage")
	}
}

func TestJudgeCancellationReachesProvider(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := Judge(ctx, "typesafe", "key", server.URL, "model", judgeBoundaryRequest())
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation was replaced: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("judgment did not stop after cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not observe cancellation")
	}
}
