package llm_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maomeng/aim/app/llm-gateway/internal/llm"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/stretchr/testify/require"
)

// Eino's component only represents choice 0. The RPC promises every choice,
// including streams whose first frame contains only a nonzero choice.
func TestChatPreservesNonzeroChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"choices\",\"choices\":[{\"index\":1,\"delta\":{\"content\":\"second\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer server.Close()
	req := &pb.ChatReq{Messages: []*pb.Message{{Role: "user", Content: "two choices"}}}
	cfg := &pb.ModelExecutionConfig{ModelName: "choices", Provider: "openai", BaseUrl: server.URL}
	resp, err := llm.CompleteChat(t.Context(), req, cfg, "fixture-not-a-secret")
	require.NoError(t, err)
	require.Len(t, resp.Choices, 2)
	require.Equal(t, int32(0), resp.Choices[0].Index)
	require.Equal(t, "first", resp.Choices[0].Message.Content)
	require.Equal(t, int32(1), resp.Choices[1].Index)
	require.Equal(t, "second", resp.Choices[1].Message.Content)
}

type cacheScopeTransport struct {
	scopes []string
}

func (t *cacheScopeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body struct {
		Scope string `json:"prompt_cache_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, err
	}
	if err := r.Body.Close(); err != nil {
		return nil, err
	}
	t.scopes = append(t.scopes, body.Scope)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))}, nil
}

func TestChatNativeCacheScopeIsStableAndIsolated(t *testing.T) {
	transport := &cacheScopeTransport{}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original })
	owner, bot := "owner-a", "bot-a"
	req := &pb.ChatReq{OwnerId: &owner, BotId: &bot, CacheScope: "interaction-a", Messages: []*pb.Message{{Role: "user", Content: "private prompt"}}}
	cfg := &pb.ModelExecutionConfig{ModelId: "model-a", ModelName: "fixture", Provider: "openai", BaseUrl: "https://api.openai.com/v1", Version: "snapshot-a"}
	call := func(key string) string {
		t.Helper()
		_, err := llm.CompleteChat(t.Context(), req, cfg, key)
		require.NoError(t, err)
		scope := transport.scopes[len(transport.scopes)-1]
		require.Regexp(t, "^[0-9a-f]{64}$", scope)
		require.NotContains(t, scope, key)
		return scope
	}
	baseline := call("credential-a")
	require.Equal(t, baseline, call("credential-a"))
	require.NotEqual(t, baseline, call("credential-b"))
	owner = "owner-b"
	require.NotEqual(t, baseline, call("credential-a"))
	owner, bot = "owner-a", "bot-b"
	require.NotEqual(t, baseline, call("credential-a"))
	bot, req.CacheScope = "bot-a", "interaction-b"
	require.NotEqual(t, baseline, call("credential-a"))
	req.CacheScope, cfg.Version = "interaction-a", "snapshot-b"
	require.NotEqual(t, baseline, call("credential-a"))
}

func TestChatAcceptsKeepalivesWithoutInventingCompletion(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\n"); err != nil {
					t.Errorf("write provider delta: %v", err)
					return
				}
				if _, err := fmt.Fprint(w, strings.Repeat(": keepalive\n\ndata: \n\n", 400)); err != nil {
					t.Errorf("write provider keepalives: %v", err)
					return
				}
				if complete {
					if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
						t.Errorf("write provider completion: %v", err)
					}
				}
			}))
			defer server.Close()
			req := &pb.ChatReq{Messages: []*pb.Message{{Role: "user", Content: "keepalives"}}}
			cfg := &pb.ModelExecutionConfig{ModelName: "fixture", Provider: "openai", BaseUrl: server.URL}
			resp, err := llm.CompleteChat(t.Context(), req, cfg, "fixture-not-a-secret")
			if complete {
				require.NoError(t, err)
				require.Equal(t, "answer", resp.Choices[0].Message.Content)
			} else {
				require.Error(t, err)
				require.Nil(t, resp)
			}
		})
	}
}
