package run

import (
	"context"
	"strings"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

const boundedDeliveryInstruction = "Execution budget requires final delivery now. Do not call tools. Preserve the accepted goal and confirmed constraints. Deliver only supported conclusions with actual citations, distinguish inference and unresolved conflicts, and state unfinished scope and omitted evidence. Do not claim exhaustive search or that empty results prove absence."

// Delivery uses the same valid checkpoint and durable tail, but spends no more
// model calls on compression. Accepted goals and constraints are never cut.
func (s *Service) deliveryModelView(ctx context.Context, r *model.Run, config ExecutionConfig) ([]*llm.Message, int32, error) {
	blocks, err := s.repo.Trajectory(ctx, r.ID)
	if err != nil {
		return nil, 0, err
	}
	checkpoint, err := s.currentCompaction(ctx, r, config)
	if err != nil {
		return nil, 0, err
	}
	messages, omissions, err := compactionView(blocks, checkpoint, config)
	if err != nil {
		return nil, 0, err
	}
	messages = append(messages, &llm.Message{Role: "system", Content: boundedDeliveryInstruction})
	withoutTools := config
	withoutTools.Tools = nil
	// First retain all bounded evidence. If it cannot fit, replace only untrusted
	// oversized materials with explicit omission notices, keeping tool pairing.
	for _, limit := range []int{config.MaxContextTokens, max(256, config.MaxContextTokens/16), 0} {
		for i, message := range messages {
			bounded, notice := boundedWorkMessage(message, int64(i+1), limit)
			if notice != "" {
				omissions = append(omissions, notice)
			}
			messages[i] = bounded
		}
		cost, err := modelInputCost(messages, withoutTools)
		if err != nil {
			return nil, 0, err
		}
		available := config.MaxContextTokens - cost - 64
		if available >= min(128, int(config.MaxOutputTokens)) {
			if len(omissions) > 0 {
				notice := strings.Join(omissions, "\n")
				updated, err := s.repo.Change(ctx, r.ID, r.ExecutionGeneration, func(_ *gorm.DB, current *model.Run) error {
					if !strings.Contains(current.ContextNotice, notice) {
						if current.ContextNotice != "" {
							current.ContextNotice += "\n"
						}
						current.ContextNotice += notice
					}
					return nil
				})
				if err != nil {
					return nil, 0, err
				}
				*r = *updated
			}
			return messages, int32(min(available, int(config.MaxOutputTokens))), nil
		}
	}
	return nil, 0, status.Error(codes.ResourceExhausted, "accepted goal and constraints exceed delivery context budget")
}
