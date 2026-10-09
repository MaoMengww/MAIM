package server

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/logic/bot"
	"github.com/maomeng/aim/app/bot-service/internal/run"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	accepted, err := s.runs.Accept(ctx, run.Input{BotID: req.BotId, ConvID: req.ConvId, RequesterID: runtimeUserID(ctx), SubmissionKey: req.SubmissionKey, Message: req.Message, ReplyToMsgID: req.ReplyToMsgId, HistoryGrantID: req.HistoryGrantId, ParentRunID: req.ParentRunId, MessageIDs: req.MessageIds, FileIDs: req.FileIds, Language: language})
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

func (s *RuntimeServer) ExecutorUpdate(ctx context.Context, req *pb.ExecutorUpdateReq) (*pb.RunResponse, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.ExecutorUpdate(forwardRuntimeContext(ctx), req)
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "executor update required")
	}
	tokenLogic := bot.NewValidateBotTokenLogic(ctx, s.svcCtx)
	validate := tokenLogic.ValidateBotToken
	if run.IsStopConfirmation(req.Body) {
		validate = tokenLogic.ValidateExecutorStopToken
	}
	authenticated, err := validate(&pb.ValidateBotTokenReq{Token: req.Token})
	if err != nil {
		return nil, err
	}
	if !authenticated.Valid || (req.BotId != "" && authenticated.GetBotId() != req.BotId) || authenticated.Type != "third_party" {
		return nil, status.Error(codes.Unauthenticated, "invalid executor credential")
	}
	receipt, err := s.runs.ExecutorUpdate(ctx, authenticated.GetBotId(), req.Body)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	return &pb.RunResponse{RunJson: string(raw)}, nil
}

func (s *RuntimeServer) ControlRun(ctx context.Context, req *pb.ControlRunReq) (*pb.RunResponse, error) {
	if s.svcCtx.RuntimeClient != nil {
		return s.svcCtx.RuntimeClient.ControlRun(forwardRuntimeContext(ctx), req)
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "Run command required")
	}
	receipt, err := s.runs.Control(ctx, req.RunId, runtimeUserID(ctx), run.ControlInput{Operation: req.Operation, Message: req.Message, SubmissionKey: req.SubmissionKey, SourceMessageID: req.SourceMessageId, MessageIDs: req.MessageIds, FileIDs: req.FileIds, HistoryGrantID: req.HistoryGrantId, TargetUserID: req.GetTargetUserId()})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	return &pb.RunResponse{RunJson: string(raw)}, nil
}
