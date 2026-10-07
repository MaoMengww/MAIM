package llm

import (
	"context"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/pkg/logx"
)

type BillingInfo struct {
	BotID      *string
	OwnerID    *string
	Capability string
	ModelEntry *domain.ModelEntry
}

type billingKeyType struct{}

var billingKey = billingKeyType{}

func WithBillingInfo(ctx context.Context, bi *BillingInfo) context.Context {
	return context.WithValue(ctx, billingKey, bi)
}

func NewBillingCallbackHandler(billingRecorder domain.BillingRecorder) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			logger := logx.DefaultLogger().WithContext(ctx)
			if info == nil || info.Component != components.ComponentOfChatModel {
				return ctx
			}
			bi, _ := ctx.Value(billingKey).(*BillingInfo)
			if bi == nil || bi.ModelEntry == nil {
				return ctx
			}

			cbOutput := einoModel.ConvCallbackOutput(output)
			if cbOutput == nil || cbOutput.TokenUsage == nil {
				return ctx
			}
			if cbOutput.TokenUsage.TotalTokens <= 0 && cbOutput.TokenUsage.PromptTokens <= 0 && cbOutput.TokenUsage.CompletionTokens <= 0 {
				return ctx
			}

			record, err := domain.NewBillingRecord(bi.ModelEntry, bi.BotID, bi.OwnerID, bi.Capability, &domain.UsageInfo{
				PromptTokens:     cbOutput.TokenUsage.PromptTokens,
				CompletionTokens: cbOutput.TokenUsage.CompletionTokens,
				TotalTokens:      cbOutput.TokenUsage.TotalTokens,
			})
			if err != nil {
				logger.Errorf("failed to allocate billing identity: %v", err)
				return ctx
			}
			if err := billingRecorder.Record(record); err != nil {
				logger.Errorf("failed to record billing for model=%s capability=%s: %v",
					bi.ModelEntry.ID, bi.Capability, err)
			}
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			logger := logx.DefaultLogger().WithContext(ctx)
			if info != nil {
				logger.Errorf("llm call failed: component=%s name=%s err=%v", info.Component, info.Name, err)
			}
			return ctx
		}).
		Build()
}
