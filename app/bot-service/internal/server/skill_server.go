package server

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/client"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SkillOperation executes the Bot-scoped skill governance API on the control
// replica. It owns the LLM domain connection for representative evaluation and
// never delegates governance to a runtime replica.
func (s *RuntimeServer) SkillOperation(ctx context.Context, req *pb.SkillOperationReq) (*pb.SkillOperationResponse, error) {
	if req == nil || req.BotId == "" {
		return nil, status.Error(codes.InvalidArgument, "Bot identity is required")
	}
	service := skill.NewService(s.svcCtx.DB.DB, client.NewLlmGatewayClient(s.svcCtx.LlmGatewayConn))
	body, err := service.Operation(ctx, runtimeUserID(ctx), req.BotId, req.Operation, req.Body)
	if err != nil {
		return nil, err
	}
	return &pb.SkillOperationResponse{Body: body}, nil
}
