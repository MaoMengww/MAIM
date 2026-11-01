package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashScopeRerankAcceptsNativeAndCompatibleResponses(t *testing.T) {
	for name, body := range map[string]string{
		"native":            `{"output":{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}]},"usage":{"total_tokens":12}}`,
		"openai-compatible": `{"object":"list","results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.2}],"usage":{"total_tokens":12}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/reranks" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			result, err := dashscopeRerank(context.Background(), "key", server.URL, "qwen3-rerank", "query", []string{"a", "b"}, 2, false)
			if err != nil {
				t.Fatal(err)
			}
			if result.Tokens != 12 || len(result.Items) != 2 || result.Items[0].Index != 1 || result.Items[0].RelevanceScore != .9 {
				t.Fatalf("unexpected rerank result: %+v", result)
			}
		})
	}
}

func TestDashScopeRerankRejectsOutOfRangeIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":2,"relevance_score":0.5}],"usage":{"total_tokens":3}}`))
	}))
	defer server.Close()
	if _, err := dashscopeRerank(context.Background(), "key", server.URL, "qwen3-rerank", "query", []string{"a", "b"}, 1, false); err == nil {
		t.Fatal("expected an out-of-range index to be rejected")
	}
}
