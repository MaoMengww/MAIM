package handler

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/llm"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"google.golang.org/grpc/status"
)

// Judge is an independent capability; its caller owns the business interpretation.
func (h *LLMGatewayHandler) Judge(ctx context.Context, req *pb.JudgeReq) (*pb.JudgeResp, error) {
	if req == nil {
		return nil, errors.ErrInvalidParam
	}
	info, err := h.resolveModel(ctx, req.ModelId, req.BotId, req.OwnerId, "judge")
	if err != nil {
		return nil, err
	}
	if err := validateJudgmentModel(info.Capability, info.Provider, info.ModelName, info.InputPricePerMTok, info.OutputPricePerMTok); err != nil {
		return nil, err
	}
	if err := llm.ValidateJudgeRequest(req); err != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid judgment request")
	}
	if info.TrackBilling {
		if err := h.checkBalance(ctx, req.OwnerId); err != nil {
			return nil, err
		}
	}
	model := info.ModelName
	if err := h.svcCtx.RateLimiter.Allow(ctx, model); err != nil {
		return nil, err
	}
	if err := h.svcCtx.RateLimiter.IncrConcurrency(ctx, model); err != nil {
		return nil, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := h.svcCtx.RateLimiter.DecrConcurrency(releaseCtx, model); err != nil {
			h.svcCtx.Logger.Errorf("judge concurrency release failed: model_id=%s", info.ID)
		}
	}()
	result, callErr := llm.Judge(ctx, info.Provider, info.APIKey, info.BaseURL, model, req)
	// Usage is a provider fact, independent of answer validity or downstream success.
	if result != nil && result.Usage != nil {
		usage := result.Usage
		if usage.InputTokens != nil || usage.OutputTokens != nil {
			record, recordErr := domain.NewBillingRecord(info.ModelEntry, req.BotId, req.OwnerId, "judge", &domain.UsageInfo{
				PromptTokens: int(usage.GetInputTokens()), CompletionTokens: int(usage.GetOutputTokens()),
			})
			if recordErr == nil {
				if usage.InputTokens == nil {
					record.InputTokens = nil
				}
				if usage.OutputTokens == nil {
					record.OutputTokens = nil
				}
				recordErr = h.svcCtx.BillingRepo.Record(record)
			}
			if recordErr != nil {
				h.svcCtx.Logger.Errorf("record judgment billing failed: model_id=%s", info.ID)
			}
			if info.TrackBilling {
				// Finish accounting even when the caller canceled after provider usage arrived.
				billingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				h.deductBalance(billingCtx, req.OwnerId, h.calcCost(info, int(usage.GetInputTokens()), int(usage.GetOutputTokens())))
				cancel()
			}
		}
	}
	if ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if callErr != nil {
		h.svcCtx.Logger.Errorf("method=Judge model_id=%s judgment_failed=true", info.ID)
		return nil, errors.New(errors.CodeRPCError, "judgment provider request failed")
	}
	return result, nil
}
