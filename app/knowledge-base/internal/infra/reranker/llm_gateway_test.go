package reranker_test

import (
	"context"
	"math"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/reranker"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type judgeServer struct {
	pb.UnimplementedLLMGatewayServer
	judge func(context.Context, *pb.JudgeReq) (*pb.JudgeResp, error)
}

func (s *judgeServer) Judge(ctx context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
	return s.judge(ctx, req)
}

func gatewayClient(t *testing.T, judge func(context.Context, *pb.JudgeReq) (*pb.JudgeResp, error)) zrpc.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterLLMGatewayServer(server, &judgeServer{judge: judge})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := zrpc.NewClient(zrpc.RpcClientConf{Target: listener.Addr().String(), NonBlock: true, Timeout: 10000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Conn().Close() })
	return client
}

func judgeRequest(n int) *domain.RerankRequest {
	candidates := make([]domain.RerankCandidate, n)
	for i := range n {
		candidates[i] = domain.RerankCandidate{Index: i, Content: "passage"}
	}
	return &domain.RerankRequest{Method: domain.RerankMethodJudge, ModelID: "01932d40-6380-7000-8000-000000000001", Query: "query", Candidates: candidates, TopK: n}
}

func TestJudgmentBusinessFanoutIsNotHardFourSupplySlots(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	client := gatewayClient(t, func(ctx context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		answers := make(map[string]*pb.JudgeAnswer, len(req.Questions))
		for name := range req.Questions {
			answers[name] = &pb.JudgeAnswer{Kind: &pb.JudgeAnswer_Noul{Noul: &pb.NoulAnswer{Probability: 0.5}}}
		}
		return &pb.JudgeResp{Answers: answers}, nil
	})
	r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 8})
	done := make(chan error, 1)
	go func() { _, err := r.Rerank(t.Context(), judgeRequest(8)); done <- err }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	started := 0
	for started < 8 {
		select {
		case <-entered:
			started++
		case <-timer.C:
			close(release)
			<-done
			t.Fatalf("configured business fanout 8 started only %d judgments; supply admission belongs to LLM domain", started)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func equalAnswers(req *pb.JudgeReq) *pb.JudgeResp {
	answers := make(map[string]*pb.JudgeAnswer, len(req.Questions))
	for name := range req.Questions {
		answers[name] = &pb.JudgeAnswer{Kind: &pb.JudgeAnswer_Noul{Noul: &pb.NoulAnswer{Probability: 0.5}}}
	}
	return &pb.JudgeResp{Answers: answers}
}

func TestJudgmentRanksEveryCandidateWithStableTies(t *testing.T) {
	var calls atomic.Int32
	client := gatewayClient(t, func(_ context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
		calls.Add(1)
		if len(req.Questions) != 1 {
			return nil, status.Error(codes.Internal, "expected one independent question")
		}
		resp := equalAnswers(req)
		if strings.HasSuffix(req.State, "Passage:\nthird") {
			resp.Answers["evidence_v1"].GetNoul().Probability = 0.9
		}
		return resp, nil
	})
	r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 8})
	req := judgeRequest(3)
	req.Candidates[0].Content = "first"
	req.Candidates[1].Content = "second"
	req.Candidates[2].Content = "third"
	results, err := r.Rerank(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(results) != 3 || results[0].Content != "third" || results[1].Content != "first" || results[2].Content != "second" {
		t.Fatalf("unexpected ranking: calls=%d results=%v", calls.Load(), results)
	}
}

func TestJudgmentPreservesFullUTF8Evidence(t *testing.T) {
	req := judgeRequest(8)
	for i := range req.Candidates {
		req.Candidates[i].Content = strings.Repeat("证据", 1500) + "\"\\\n"
	}
	var calls atomic.Int32
	client := gatewayClient(t, func(_ context.Context, work *pb.JudgeReq) (*pb.JudgeResp, error) {
		calls.Add(1)
		if work.State != "Query:\n"+req.Query+"\n\nPassage:\n"+req.Candidates[0].Content || len(work.Questions) != 1 {
			return nil, status.Error(codes.Internal, "evidence truncated or combined")
		}
		return equalAnswers(work), nil
	})
	r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 3})
	results, err := r.Rerank(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 8 || len(results) != 8 {
		t.Fatalf("incomplete scores: calls=%d results=%d", calls.Load(), len(results))
	}
	for i, result := range results {
		if result.Index != i || result.Content != req.Candidates[i].Content {
			t.Fatal("completion reordered tied evidence")
		}
	}
}

func TestJudgmentNeverReturnsPartialRanking(t *testing.T) {
	for _, fault := range []string{"missing", "unknown", "wrong_kind", "nan", "out_of_range", "rpc"} {
		t.Run(fault, func(t *testing.T) {
			client := gatewayClient(t, func(_ context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
				resp := equalAnswers(req)
				switch fault {
				case "missing":
					delete(resp.Answers, "evidence_v1")
				case "unknown":
					resp.Answers["unrequested"] = resp.Answers["evidence_v1"]
					delete(resp.Answers, "evidence_v1")
				case "wrong_kind":
					resp.Answers["evidence_v1"] = &pb.JudgeAnswer{}
				case "nan":
					resp.Answers["evidence_v1"].GetNoul().Probability = math.NaN()
				case "out_of_range":
					resp.Answers["evidence_v1"].GetNoul().Probability = 1.1
				case "rpc":
					return nil, status.Error(codes.Unavailable, "provider unavailable")
				}
				return resp, nil
			})
			r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 2})
			results, err := r.Rerank(t.Context(), judgeRequest(3))
			if err == nil || results != nil {
				t.Fatalf("partial ranking accepted: results=%v err=%v", results, err)
			}
		})
	}
}

func TestJudgmentOversizeAndInvalidUTF8FailBeforeDispatch(t *testing.T) {
	for _, passage := range []string{strings.Repeat("界", 11000), string([]byte{0xff})} {
		var calls atomic.Int32
		client := gatewayClient(t, func(_ context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
			calls.Add(1)
			return equalAnswers(req), nil
		})
		req := judgeRequest(2)
		req.Candidates[1].Content = passage
		r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 2})
		results, err := r.Rerank(t.Context(), req)
		if err == nil || len(results) != 0 || calls.Load() != 0 {
			t.Fatalf("unsafe dispatch accepted: calls=%d err=%v", calls.Load(), err)
		}
	}
}

func TestJudgmentAuthorizationFailureAndCancellationAbort(t *testing.T) {
	var calls atomic.Int32
	client := gatewayClient(t, func(_ context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
		calls.Add(1)
		return equalAnswers(req), nil
	})
	r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 2})
	req := judgeRequest(2)
	req.Authorize = func(context.Context) error { return status.Error(codes.PermissionDenied, "revoked") }
	_, err := r.Rerank(t.Context(), req)
	if status.Code(err) != codes.PermissionDenied || !domain.RerankMustAbort(err) || calls.Load() != 0 {
		t.Fatalf("authorization was hidden: %v", err)
	}
	req.Authorize = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = r.Rerank(ctx, req)
	if status.Code(err) != codes.Canceled || !domain.RerankMustAbort(err) || calls.Load() != 0 {
		t.Fatalf("cancellation was hidden: %v", err)
	}
}

func TestJudgmentUsesAllCandidatesOrFails(t *testing.T) {
	var calls atomic.Int32
	client := gatewayClient(t, func(_ context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
		if calls.Add(1) == 2 {
			return nil, status.Error(codes.Unavailable, "second candidate failed")
		}
		return equalAnswers(req), nil
	})
	req := judgeRequest(8)
	for i := range req.Candidates {
		req.Candidates[i].Content = strings.Repeat("x", 10000)
	}
	r := reranker.NewLLMGatewayReranker(client, reranker.JudgmentConfig{Fanout: 1})
	results, err := r.Rerank(t.Context(), req)
	if err == nil || !slices.Equal(results, []domain.RerankResult(nil)) || calls.Load() != 2 {
		t.Fatalf("partial scores accepted: calls=%d results=%v err=%v", calls.Load(), results, err)
	}
}
