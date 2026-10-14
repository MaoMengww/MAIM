package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
)

const chatFixtureKey = "fixture-chat-key-not-a-secret"

// The provider is real HTTP; only registry, quota, and billing storage are mocked.
// Both public handler methods consume the same SSE fixture.
const chatFixtureDelta = `data: {"id":"fixture-completion","object":"chat.completion.chunk","model":"fixture-chat","choices":[{"index":0,"delta":{"role":"assistant","content":"answer"},"finish_reason":null}]}` + "\n\n"
const chatFixtureFinish = `data: {"id":"fixture-completion","object":"chat.completion.chunk","model":"fixture-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
const chatFixtureDone = "data: [DONE]\n\n"

func chatFixtureUsage(usage string) string {
	if usage == "" {
		return ""
	}
	return `data: {"id":"fixture-completion","object":"chat.completion.chunk","model":"fixture-chat","choices":[],"usage":` + usage + "}\n\n"
}

func chatFixtureSSE(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, body)
}

type chatQuota struct {
	mockRateLimiter
	mu         sync.Mutex
	active     int
	acquired   int
	released   int
	releaseErr error
}

func (q *chatQuota) IncrConcurrency(ctx context.Context, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.active != 0 {
		return errors.New(errors.CodeTooManyRequests, "fixture concurrency exhausted")
	}
	q.active++
	q.acquired++
	return nil
}

func (q *chatQuota) DecrConcurrency(ctx context.Context, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.releaseErr = ctx.Err()
	if q.releaseErr != nil {
		return q.releaseErr
	}
	q.active--
	q.released++
	return nil
}

func (q *chatQuota) assertReleased(t *testing.T, calls int) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	require.NoError(t, q.releaseErr, "quota cleanup must not inherit cancellation")
	require.Zero(t, q.active, "quota must be available for the next invocation")
	require.Equal(t, calls, q.acquired)
	require.Equal(t, calls, q.released)
}

type chatBalance struct {
	userpb.UserServiceClient
	mu         sync.Mutex
	deductions []*userpb.DeductBalanceReq
}

func (b *chatBalance) GetBalance(context.Context, *userpb.GetBalanceReq, ...grpc.CallOption) (*userpb.GetBalanceResp, error) {
	return &userpb.GetBalanceResp{Balance: 10}, nil
}

func (b *chatBalance) DeductBalance(_ context.Context, req *userpb.DeductBalanceReq, _ ...grpc.CallOption) (*userpb.DeductBalanceResp, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deductions = append(b.deductions, proto.Clone(req).(*userpb.DeductBalanceReq))
	return &userpb.DeductBalanceResp{NewBalance: 9}, nil
}

type chatStreamRecorder struct {
	grpc.ServerStream
	ctx    context.Context
	chunks []*pb.ChatStreamChunk
}

func (s *chatStreamRecorder) Context() context.Context { return s.ctx }
func (s *chatStreamRecorder) Send(chunk *pb.ChatStreamChunk) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.chunks = append(s.chunks, proto.Clone(chunk).(*pb.ChatStreamChunk))
	return nil
}

type chatHandlerFixture struct {
	handler *LLMGatewayHandler
	repo    *mockModelRepo
	billing *mockBillingRepo
	quota   *chatQuota
	balance *chatBalance
	req     *pb.ChatReq
	ctx     context.Context
	calls   atomic.Int64
}

func newChatHandlerFixture(t *testing.T, provider http.HandlerFunc) *chatHandlerFixture {
	t.Helper()
	f := &chatHandlerFixture{quota: &chatQuota{}, balance: &chatBalance{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "fixture route not found", http.StatusNotFound)
			return
		}
		provider(w, r)
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	svcCtx := newMockServiceContext()
	f.repo = svcCtx.ModelRepo.(*mockModelRepo)
	f.billing = svcCtx.BillingRepo.(*mockBillingRepo)
	svcCtx.RateLimiter, svcCtx.UserClient = f.quota, f.balance
	modelID := f.repo.add(t, "fixture-chat", "openai", "chat", "active", chatFixtureKey, server.URL+"/v1", 2, 4)
	ownerID, err := identity.New()
	require.NoError(t, err)
	botID, err := identity.New()
	require.NoError(t, err)
	f.req = &pb.ChatReq{ModelId: modelID, OwnerId: &ownerID, BotId: &botID, Messages: []*pb.Message{{Role: "user", Content: "fixture prompt"}}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("user-id", ownerID))
	f.ctx, err = interceptor.WithUserIDFromMetadata(ctx)
	require.NoError(t, err)
	f.handler = NewLLMGatewayHandler(svcCtx)
	return f
}

func (f *chatHandlerFixture) freeze(t *testing.T) {
	t.Helper()
	cfg, err := f.handler.ResolveExecutionModel(f.ctx, &pb.ResolveExecutionModelReq{ModelId: f.req.ModelId, BotId: f.req.BotId, OwnerId: f.req.OwnerId})
	require.NoError(t, err)
	f.req.ExecutionModel = cfg
}

func (f *chatHandlerFixture) changeModel(change func(*domain.ModelEntry)) {
	f.repo.mu.Lock()
	defer f.repo.mu.Unlock()
	change(f.repo.byID[f.req.ModelId])
}

func (f *chatHandlerFixture) call(ctx context.Context, stream bool) (*pb.ChatResp, []*pb.ChatStreamChunk, error) {
	if !stream {
		resp, err := f.handler.Chat(ctx, f.req)
		return resp, nil, err
	}
	recorder := &chatStreamRecorder{ctx: ctx}
	err := f.handler.ChatStream(f.req, recorder)
	return nil, recorder.chunks, err
}

func chatObservedUsage(resp *pb.ChatResp, chunks []*pb.ChatStreamChunk) *pb.UsageInfo {
	if resp != nil {
		return resp.Usage
	}
	var usage *pb.UsageInfo
	for _, chunk := range chunks {
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	return usage
}

func assertChatAnswer(t *testing.T, resp *pb.ChatResp, chunks []*pb.ChatStreamChunk) {
	t.Helper()
	if resp != nil {
		require.Len(t, resp.Choices, 1)
		require.Equal(t, "answer", resp.Choices[0].GetMessage().GetContent())
		require.Equal(t, "stop", resp.Choices[0].FinishReason)
		return
	}
	var content strings.Builder
	var finish string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			content.WriteString(choice.GetDelta().GetContent())
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
	}
	require.Equal(t, "answer", content.String())
	require.Equal(t, "stop", finish)
}

func TestChatHandlersRejectStaleCredentialRoutes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, route := range []string{"endpoint", "provider"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, route), func(t *testing.T) {
				var replacementCalls atomic.Int64
				replacement := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					replacementCalls.Add(1)
					chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
				}))
				t.Cleanup(replacement.Close)
				f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
				})
				f.freeze(t)
				f.changeModel(func(entry *domain.ModelEntry) {
					entry.APIKey = "fixture-replacement-key"
					if route == "endpoint" {
						entry.BaseURL = replacement.URL + "/v1"
					} else {
						entry.Provider = "deepseek"
					}
				})
				_, _, err := f.call(f.ctx, stream)
				assertErrorCode(t, err, errors.CodeInvalidParam)
				require.Zero(t, f.calls.Load(), "old route must never receive replacement credentials")
				require.Zero(t, replacementCalls.Load(), "snapshot must not silently reroute")
				f.quota.assertReleased(t, 0)
			})
		}
	}
}

func TestChatHandlersRejectRotatedCredentialUntilNewSnapshot(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			f := newChatHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-rotated-key" {
					http.Error(w, "fixture authentication failed", http.StatusUnauthorized)
					return
				}
				chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
			})
			f.freeze(t)
			f.changeModel(func(entry *domain.ModelEntry) { entry.APIKey = "fixture-rotated-key" })
			resp, chunks, err := f.call(f.ctx, stream)
			assertErrorCode(t, err, errors.CodeInvalidParam)
			require.Nil(t, resp)
			require.Empty(t, chunks)
			require.Zero(t, f.calls.Load(), "stale credential snapshot must not reach the provider")
			f.quota.assertReleased(t, 0)
			f.freeze(t)
			resp, chunks, err = f.call(f.ctx, stream)
			require.NoError(t, err)
			assertChatAnswer(t, resp, chunks)
			require.Equal(t, int64(1), f.calls.Load())
			f.quota.assertReleased(t, 1)
		})
	}
}

func TestChatHandlersRecheckSnapshotAuthority(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *chatHandlerFixture)
		code   int
	}{
		{"forged_hmac", func(_ *testing.T, f *chatHandlerFixture) {
			f.req.ExecutionModel.Version = "forged-fixture-signature"
		}, errors.CodeInvalidParam},
		{"signed_route_tampered", func(_ *testing.T, f *chatHandlerFixture) {
			f.req.ExecutionModel.BaseUrl += "/tampered"
		}, errors.CodeInvalidParam},
		{"disabled_since_resolution", func(_ *testing.T, f *chatHandlerFixture) {
			f.changeModel(func(entry *domain.ModelEntry) { entry.Status = "disabled" })
		}, errors.CodeForbidden},
		{"owner_visibility_revoked", func(t *testing.T, f *chatHandlerFixture) {
			otherOwner, err := identity.New()
			require.NoError(t, err)
			f.changeModel(func(entry *domain.ModelEntry) { entry.OwnerType, entry.OwnerID = "user", &otherOwner })
		}, errors.CodeForbidden},
		{"caller_owner_mismatch", func(t *testing.T, f *chatHandlerFixture) {
			otherOwner, err := identity.New()
			require.NoError(t, err)
			f.req.OwnerId = &otherOwner
		}, errors.CodeForbidden},
		{"invalid_bot_reference", func(_ *testing.T, f *chatHandlerFixture) {
			invalid := "invalid-fixture-bot"
			f.req.BotId = &invalid
		}, errors.CodeInvalidParam},
		{"snapshot_reused_for_another_bot", func(t *testing.T, f *chatHandlerFixture) {
			otherBot, err := identity.New()
			require.NoError(t, err)
			f.req.BotId = &otherBot
		}, errors.CodeInvalidParam},
	}
	for _, stream := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
				})
				f.freeze(t)
				tc.change(t, f)
				_, _, err := f.call(f.ctx, stream)
				assertErrorCode(t, err, tc.code)
				require.Zero(t, f.calls.Load(), "authorization must happen before provider invocation")
				f.quota.assertReleased(t, 0)
			})
		}
	}
}

func TestChatHandlersUsagePresenceAndBilling(t *testing.T) {
	cases := []struct {
		name       string
		usage      string
		prompt     int32
		completion int32
		cached     *int32
	}{
		{name: "missing"},
		{name: "explicit_zero", usage: `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`},
		{name: "reported", usage: `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2}}`, prompt: 12, completion: 3, cached: proto.Int32(2)},
	}
	for _, stream := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureUsage(tc.usage)+chatFixtureDone)
				})
				resp, chunks, err := f.call(f.ctx, stream)
				require.NoError(t, err)
				assertChatAnswer(t, resp, chunks)
				usage := chatObservedUsage(resp, chunks)
				f.billing.mu.Lock()
				defer f.billing.mu.Unlock()
				f.balance.mu.Lock()
				defer f.balance.mu.Unlock()
				if tc.usage == "" {
					require.Nil(t, usage, "an omitted provider usage is not reported zero")
					require.Empty(t, f.billing.records)
					require.Empty(t, f.balance.deductions)
				} else {
					require.NotNil(t, usage)
					require.Equal(t, tc.prompt, usage.PromptTokens)
					require.Equal(t, tc.completion, usage.CompletionTokens)
					require.Equal(t, tc.prompt+tc.completion, usage.TotalTokens)
					require.Equal(t, tc.cached, usage.CachedInputTokens)
					require.Nil(t, usage.CacheWriteTokens, "unsupported cache writes must stay absent")
					require.Len(t, f.billing.records, 1, "explicit zero still records a reported usage")
					record := f.billing.records[0]
					require.Equal(t, int(tc.prompt), record.InputTokens)
					require.Equal(t, int(tc.completion), record.OutputTokens)
					require.Equal(t, *f.req.OwnerId, *record.OwnerID)
					cost := float64(tc.prompt)*2/1_000_000 + float64(tc.completion)*4/1_000_000
					require.InDelta(t, cost, record.InputCost+record.OutputCost, 1e-12)
					if cost == 0 {
						require.Empty(t, f.balance.deductions, "zero usage must not debit the account")
					} else {
						require.Len(t, f.balance.deductions, 1)
						require.Equal(t, *f.req.OwnerId, f.balance.deductions[0].UserId)
						require.InDelta(t, cost, f.balance.deductions[0].Amount, 1e-12)
					}
					if stream {
						var usageOnly *pb.ChatStreamChunk
						for _, chunk := range chunks {
							if chunk.Usage != nil && len(chunk.Choices) == 0 {
								usageOnly = chunk
							}
						}
						require.NotNil(t, usageOnly, "usage-only provider event must reach the streaming consumer")
						require.True(t, proto.Equal(usage, usageOnly.Usage))
					}
				}
				f.quota.assertReleased(t, 1)
			})
		}
	}
}

func TestChatHandlersReleaseConcurrencyOnProviderFailure(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		httpStatus int
	}{
		{name: "truncated_before_finish", body: chatFixtureDelta},
		{name: "finish_without_done", body: chatFixtureDelta + chatFixtureFinish},
		{name: "done_without_finish", body: chatFixtureDelta + chatFixtureDone},
		{name: "provider_error", body: `{"error":{"message":"fixture-body-secret fixture prompt fixture-chat-key-not-a-secret","type":"server_error"}}`, httpStatus: http.StatusBadGateway},
		{name: "provider_error_midstream", body: chatFixtureDelta + `data: {"error":{"message":"fixture-body-secret fixture prompt fixture-chat-key-not-a-secret","type":"server_error"}}` + "\n\n"},
	}
	for _, stream := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				var calls atomic.Int64
				f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					if calls.Add(1) > 1 {
						chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
						return
					}
					if tc.httpStatus != 0 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(tc.httpStatus)
						_, _ = fmt.Fprint(w, tc.body)
						return
					}
					chatFixtureSSE(w, tc.body)
				})
				resp, _, err := f.call(f.ctx, stream)
				assertErrorCode(t, err, errors.CodeRPCError)
				require.Nil(t, resp, "a partial completion must never be reported as success")
				for _, sensitive := range []string{"fixture-body-secret", "fixture prompt", chatFixtureKey} {
					require.NotContains(t, err.Error(), sensitive)
				}
				f.quota.assertReleased(t, 1)
				f.billing.mu.Lock()
				require.Empty(t, f.billing.records, "partial content without usage is not billable")
				f.billing.mu.Unlock()
				resp, chunks, err := f.call(f.ctx, stream)
				require.NoError(t, err, "next request must be able to acquire the released quota")
				assertChatAnswer(t, resp, chunks)
				f.quota.assertReleased(t, 2)
			})
		}
	}
}

func TestChatHandlersCancellationReleasesLiveQuota(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			started := make(chan struct{})
			var calls atomic.Int64
			f := newChatHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) > 1 {
					chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
					return
				}
				chatFixtureSSE(w, chatFixtureDelta)
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Errorf("flush provider stream: %v", err)
				}
				close(started)
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second):
				}
			})
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, _, err := f.call(ctx, stream)
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("provider was not reached before timeout")
			}
			cancel()
			select {
			case err := <-result:
				require.Equal(t, codes.Canceled, status.Code(err))
			case <-time.After(2 * time.Second):
				t.Fatal("canceled handler did not return before timeout")
			}
			f.quota.assertReleased(t, 1)
			resp, chunks, err := f.call(f.ctx, stream)
			require.NoError(t, err, "cancellation must not consume quota for the next request")
			assertChatAnswer(t, resp, chunks)
			f.quota.assertReleased(t, 2)
		})
	}
}
