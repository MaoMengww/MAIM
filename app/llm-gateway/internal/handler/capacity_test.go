package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The public Chat seam must recheck qualification after waiting; changing a
// registry entry while awaiting supply must not send revoked work upstream.
func TestChatRevalidatesEligibilityAfterCapacityWait(t *testing.T) {
	f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
	})
	waiting := make(chan struct{})
	resume := make(chan struct{})
	permit, err := f.quota.Acquire(f.ctx, domain.AdmissionRequest{})
	require.NoError(t, err)
	f.handler.svcCtx.Capacity = &waitingCapacity{waiting: waiting, resume: resume, permit: permit}
	result := make(chan error, 1)
	go func() {
		_, err := f.handler.Chat(f.ctx, f.req)
		result <- err
	}()
	select {
	case <-waiting:
	case <-f.ctx.Done():
		t.Fatal("request never reached admission")
	}
	f.repo.mu.Lock()
	entry := *f.repo.byID[f.req.ModelId]
	entry.Status = "disabled"
	f.repo.byID[f.req.ModelId] = &entry
	f.repo.mu.Unlock()
	close(resume)
	select {
	case err := <-result:
		assertErrorCode(t, err, errors.CodeForbidden)
	case <-f.ctx.Done():
		t.Fatal("revoked request did not finish")
	}
	require.Zero(t, f.calls.Load(), "revoked work must not reach the provider")
	f.quota.assertReleased(t, 1)
	require.Equal(t, 1, f.quota.aborted, "undispatched reservation must be refunded")
}

type waitingCapacity struct {
	waiting chan struct{}
	resume  chan struct{}
	permit  domain.CapacityPermit
}

func (s *waitingCapacity) Acquire(ctx context.Context, _ domain.AdmissionRequest) (domain.CapacityPermit, error) {
	close(s.waiting)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.resume:
		return s.permit, nil
	}
}

func TestChatRunRecheckRejectionAbortsBeforeProviderDispatch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
			})
			permit, err := f.quota.Acquire(f.ctx, domain.AdmissionRequest{})
			require.NoError(t, err)
			waiting, resume := make(chan struct{}), make(chan struct{})
			f.handler.svcCtx.Capacity = &waitingCapacity{waiting: waiting, resume: resume, permit: permit}
			result := make(chan error, 1)
			go func() {
				_, _, err := f.call(f.ctx, stream)
				result <- err
			}()
			select {
			case <-waiting:
			case <-f.ctx.Done():
				t.Fatal("request never reached admission")
			}
			// First authorization accepted a non-Run caller; force the second
			// check to reject authority without involving DB or provider I/O.
			invalidRun := "invalid-run-authority"
			f.req.RunId = &invalidRun
			close(resume)
			select {
			case err := <-result:
				require.Equal(t, codes.InvalidArgument, status.Code(err))
			case <-f.ctx.Done():
				t.Fatal("Run recheck did not finish")
			}
			require.Zero(t, f.calls.Load())
			f.quota.assertReleased(t, 1)
			require.Equal(t, 1, f.quota.aborted)
		})
	}
}

type blockingRecheckRegistry struct {
	*mockModelRepo
	lookups int
	waiting chan struct{}
}

func (r *blockingRecheckRegistry) FindByID(ctx context.Context, id string) (*domain.ModelEntry, error) {
	r.lookups++
	if r.lookups == 1 {
		return r.mockModelRepo.FindByID(ctx, id)
	}
	close(r.waiting)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestChatEligibilityRecheckUsesPermitCancellation(t *testing.T) {
	f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
	})
	permitCtx, cancelPermit := context.WithCancel(f.ctx)
	defer cancelPermit()
	permit, err := f.quota.Acquire(permitCtx, domain.AdmissionRequest{})
	require.NoError(t, err)
	resume := make(chan struct{})
	close(resume)
	f.handler.svcCtx.Capacity = &waitingCapacity{waiting: make(chan struct{}), resume: resume, permit: permit}
	registry := &blockingRecheckRegistry{mockModelRepo: f.repo, waiting: make(chan struct{})}
	f.handler.svcCtx.ModelRepo = registry
	result := make(chan error, 1)
	go func() {
		_, err := f.handler.Chat(f.ctx, f.req)
		result <- err
	}()
	select {
	case <-registry.waiting:
	case <-f.ctx.Done():
		t.Fatal("eligibility recheck did not begin")
	}
	cancelPermit() // The original caller remains live; only the lease is canceled.
	select {
	case err := <-result:
		require.Error(t, err)
	case <-f.ctx.Done():
		t.Fatal("eligibility lookup ignored permit cancellation")
	}
	require.NoError(t, f.ctx.Err())
	require.Zero(t, f.calls.Load())
	f.quota.assertReleased(t, 1)
	require.Equal(t, 1, f.quota.aborted)
}
func (*waitingCapacity) Close() error { return nil }

func TestChatLocalValidationAbortsUndispatchedReservation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
			})
			messages := f.req.Messages
			f.req.Messages = nil
			_, _, err := f.call(f.ctx, stream)
			assertErrorCode(t, err, errors.CodeInvalidParam)
			require.Zero(t, f.calls.Load())
			f.quota.assertReleased(t, 1)
			require.Equal(t, 1, f.quota.aborted)
			f.req.Messages = messages
			_, _, err = f.call(f.ctx, stream)
			require.NoError(t, err)
			require.EqualValues(t, 1, f.calls.Load())
			f.quota.assertReleased(t, 2)
			require.Equal(t, 1, f.quota.aborted, "dispatched work must not be refunded")
		})
	}
}

func TestJudgeDistinguishesCallerDeadlineFromProviderGatewayTimeout(t *testing.T) {
	for _, callerExpires := range []bool{false, true} {
		t.Run(fmt.Sprintf("callerExpires=%t", callerExpires), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if callerExpires {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = fmt.Fprint(w, `{"detail":"provider-side deadline, not caller expiry"}`)
			}))
			defer server.Close()
			defer server.CloseClientConnections()
			svcCtx := newMockServiceContext()
			modelID := svcCtx.ModelRepo.(*mockModelRepo).add(t, "jev-fixed-version", "typesafe", "judge", "active", "fixture-key", server.URL, 0, 0)
			quota := &chatQuota{}
			svcCtx.Capacity = quota
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			response, err := NewLLMGatewayHandler(svcCtx).Judge(ctx, &pb.JudgeReq{
				ModelId: modelID, State: "material", Questions: map[string]*pb.JudgeQuestion{
					"relevant": {Kind: &pb.JudgeQuestion_Noul{Noul: &pb.NoulQuestion{}}},
				},
			})
			require.Nil(t, response)
			if callerExpires {
				require.Equal(t, codes.DeadlineExceeded, status.Code(err))
				require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
			} else {
				require.NoError(t, ctx.Err(), "provider 504 must happen before caller expiry")
				assertErrorCode(t, err, errors.CodeRPCError)
				bizErr, ok := errors.IsBizError(err)
				require.True(t, ok)
				require.Equal(t, codes.Unavailable, bizErr.GRPCCode())
			}
			quota.assertReleased(t, 1)
			require.Zero(t, quota.aborted, "both outcomes followed an actual provider attempt")
		})
	}
}

type rejectingCapacity struct {
	err   error
	calls int
}

func (s *rejectingCapacity) Acquire(context.Context, domain.AdmissionRequest) (domain.CapacityPermit, error) {
	s.calls++
	return nil, s.err
}

func (*rejectingCapacity) Close() error { return nil }

func TestChatCapacityBusyIsServiceUnavailableAndDoesNotDispatch(t *testing.T) {
	f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
	})
	rejecting := &rejectingCapacity{err: domain.ErrCapacityBusy}
	f.handler.svcCtx.Capacity = rejecting
	_, err := f.handler.Chat(f.ctx, f.req)
	assertErrorCode(t, err, errors.CodeServiceDown)
	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	require.Equal(t, codes.Unavailable, bizErr.GRPCCode())
	require.Equal(t, http.StatusServiceUnavailable, errors.HTTPStatusFromCode(bizErr.Code))
	require.Equal(t, 1, rejecting.calls)
	require.Zero(t, f.calls.Load())
}

func TestChatDoesNotInterpretWorkloadMetadata(t *testing.T) {
	f := newChatHandlerFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		chatFixtureSSE(w, chatFixtureDelta+chatFixtureFinish+chatFixtureDone)
	})
	ctx := metadata.NewIncomingContext(f.ctx, metadata.Pairs("x-aim-model-workload", "unknown", "x-aim-model-workload", "background"))
	_, err := f.handler.Chat(ctx, f.req)
	require.NoError(t, err)
	require.EqualValues(t, 1, f.calls.Load())
}

func TestEmbedIngestTokenIsAuthenticatedWithoutWorkloadMetadata(t *testing.T) {
	for _, tc := range []struct {
		name          string
		tokens        []string
		wantCode      int
		wantAdmission int
	}{
		{name: "online", wantCode: errors.CodeServiceDown, wantAdmission: 1},
		{name: "ingest", tokens: []string{"ingest-secret"}, wantCode: errors.CodeServiceDown, wantAdmission: 1},
		{name: "invalid", tokens: []string{"invalid"}, wantCode: errors.CodeForbidden},
		{name: "duplicate", tokens: []string{"ingest-secret", "ingest-secret"}, wantCode: errors.CodeForbidden},
		{name: "empty", tokens: []string{""}, wantCode: errors.CodeForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svcCtx := newMockServiceContext()
			svcCtx.Config.IngestEmbeddingToken = "ingest-secret"
			id := svcCtx.ModelRepo.(*mockModelRepo).add(t, "embed", "openai", "embed", "active", "key", "https://unused.invalid", 0, 0)
			rejecting := &rejectingCapacity{err: domain.ErrCapacityBusy}
			svcCtx.Capacity = rejecting
			md := metadata.MD{}
			if tc.tokens != nil {
				md["x-aim-ingest-token"] = tc.tokens
			}
			ctx := metadata.NewIncomingContext(t.Context(), md)
			_, err := NewLLMGatewayHandler(svcCtx).Embed(ctx, &pb.EmbedReq{ModelId: id, Input: []string{"input"}})
			assertErrorCode(t, err, tc.wantCode)
			require.Equal(t, tc.wantAdmission, rejecting.calls)
		})
	}
}
