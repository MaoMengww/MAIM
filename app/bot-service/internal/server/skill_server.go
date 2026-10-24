package server

import (
	"context"
	"strings"

	"github.com/maomeng/aim/app/bot-service/internal/client"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SkillOperation keeps package governance on control; source-scoped review
// operations run on runtime, which owns current material authorization clients.
func (s *RuntimeServer) SkillOperation(ctx context.Context, req *pb.SkillOperationReq) (*pb.SkillOperationResponse, error) {
	if req == nil || req.BotId == "" {
		return nil, status.Error(codes.InvalidArgument, "Bot identity is required")
	}
	if strings.HasPrefix(req.Operation, "review-") {
		if s.svcCtx.RuntimeClient != nil {
			return forwardRuntime(s, ctx, func(client pb.BotServiceClient) (*pb.SkillOperationResponse, error) {
				return client.SkillOperation(forwardRuntimeContext(ctx), req)
			})
		}
		body, err := s.runs.ReviewOperation(ctx, runtimeUserID(ctx), req.BotId, req.Operation, req.Body)
		if err != nil {
			return nil, err
		}
		return &pb.SkillOperationResponse{Body: body}, nil
	}
	service := skill.NewService(s.svcCtx.DB.DB, client.NewLlmGatewayClient(s.svcCtx.LlmGatewayConn))
	body, err := service.Operation(ctx, runtimeUserID(ctx), req.BotId, req.Operation, req.Body)
	if err != nil {
		return nil, err
	}
	return &pb.SkillOperationResponse{Body: body}, nil
}
