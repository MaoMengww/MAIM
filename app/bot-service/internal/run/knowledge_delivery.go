package run

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const boundedDeliveryInstruction = "Execution budget requires final delivery now. Do not call tools. Preserve the accepted goal and confirmed constraints. Deliver only supported conclusions with actual citations, distinguish inference and unresolved conflicts, and state unfinished scope and omitted evidence. Do not claim exhaustive search or that empty results prove absence."

// Delivery retains the exact stage prefix and tools. Its instruction is a durable
// trajectory fact so newly accepted input cannot move a transient tail message.
func (s *Service) deliveryModelView(ctx context.Context, r *model.Run, config ExecutionConfig) ([]*llm.Message, int32, error) {
	blocks, err := s.repo.Trajectory(ctx, r.ID)
	if err != nil {
		return nil, 0, err
	}
	checkpoint, err := s.currentCompaction(ctx, r, config)
	if err != nil {
		return nil, 0, err
	}
	messages, _, err := compactionView(blocks, checkpoint, config)
	if err != nil {
		return nil, 0, err
	}
	appendInstruction := !deliveryRequested(messages)
	if appendInstruction {
		messages = append(messages, &llm.Message{Role: "system", Content: boundedDeliveryInstruction})
	}
	cost, err := modelInputCost(messages, config)
	if err != nil {
		return nil, 0, err
	}
	available := config.MaxContextTokens - cost - 64
	if available < min(128, int(config.MaxOutputTokens)) {
		return nil, 0, status.Error(codes.ResourceExhausted, "work and tools exceed delivery context budget; no historical material was rewritten")
	}
	if appendInstruction {
		updated, err := s.repo.Change(ctx, r.ID, r.ExecutionGeneration, func(tx *gorm.DB, current *model.Run) error {
			if current.TrajectoryVersion != r.TrajectoryVersion || current.AppliedInputVersion != r.AppliedInputVersion {
				return status.Error(codes.Aborted, "work changed before delivery instruction")
			}
			return repo.AppendRunBlock(tx, current, "message", messages[len(messages)-1])
		})
		if err != nil {
			return nil, 0, err
		}
		*r = *updated
	}
	return messages, int32(min(available, int(config.MaxOutputTokens))), nil
}

func deliveryRequested(messages []*llm.Message) bool {
	for _, message := range messages {
		if message.Role == "system" && message.Content == boundedDeliveryInstruction {
			return true
		}
	}
	return false
}
