package server

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/run"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"google.golang.org/grpc/metadata"
)

type RuntimeServer struct {
	svcCtx *svc.ServiceContext
	runs   *run.Service
}

func NewRuntimeServer(svcCtx *svc.ServiceContext) *RuntimeServer {
	return &RuntimeServer{svcCtx: svcCtx, runs: run.NewService(svcCtx)}
}
func (s *RuntimeServer) StartRun(ctx context.Context, req *pb.StartRunReq) (*pb.RunResponse, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.StartRun(forwardRuntimeContext(ctx), req)
	}
	if req == nil {
		req = &pb.StartRunReq{}
	}
	md, _ := metadata.FromIncomingContext(ctx)
	language := ""
	if values := md.Get("x-user-language"); len(values) > 0 {
		language = values[0]
	}
	accepted, err := s.runs.Accept(ctx, run.Input{BotID: req.BotId, ConvID: req.ConvId, RequesterID: runtimeUserID(ctx), SubmissionKey: req.SubmissionKey, Message: req.Message, ReplyToMsgID: req.ReplyToMsgId, HistoryGrantID: req.HistoryGrantId, Language: language})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(accepted.Public())
	if err != nil {
		return nil, err
	}
	return &pb.RunResponse{RunJson: string(raw)}, nil
}
func (s *RuntimeServer) GetRun(ctx context.Context, req *pb.GetRunReq) (*pb.RunResponse, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.GetRun(forwardRuntimeContext(ctx), req)
	}
	if req == nil {
		req = &pb.GetRunReq{}
	}
	r, err := s.runs.Get(ctx, req.RunId, runtimeUserID(ctx))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r.Public())
	if err != nil {
		return nil, err
	}
	return &pb.RunResponse{RunJson: string(raw)}, nil
}
func (s *RuntimeServer) ListRuns(ctx context.Context, req *pb.ListRunsReq) (*pb.ListRunsResponse, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.ListRuns(forwardRuntimeContext(ctx), req)
	}
	if req == nil {
		req = &pb.ListRunsReq{}
	}
	rows, err := s.runs.List(ctx, req.ConvId, runtimeUserID(ctx), int(req.Limit))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	return &pb.ListRunsResponse{RunsJson: string(raw)}, nil
}
