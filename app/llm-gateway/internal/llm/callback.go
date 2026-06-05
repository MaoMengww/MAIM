package llm

import (
	"context"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	appModel "github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/pkg/logx"
)

type BillingInfo struct {
	BotID      int64
	OwnerID    int64
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

			inputCost := float64(cbOutput.TokenUsage.PromptTokens) / 1_000_000 * bi.ModelEntry.InputPricePerMTok
			outputCost := float64(cbOutput.TokenUsage.CompletionTokens) / 1_000_000 * bi.ModelEntry.OutputPricePerMTok

			record := &appModel.BillingRecord{
				BotID:        bi.BotID,
				OwnerID:      bi.OwnerID,
				ModelName:    bi.ModelEntry.ModelName,
				Capability:   bi.Capability,
				InputTokens:  cbOutput.TokenUsage.PromptTokens,
				OutputTokens: cbOutput.TokenUsage.CompletionTokens,
				InputCost:    inputCost,
				OutputCost:   outputCost,
				Provider:     bi.ModelEntry.Provider,
				CreatedAt:    time.Now(),
			}
			if err := billingRecorder.Record(record); err != nil {
				logger.Errorf("failed to record billing for model=%s bot=%d owner=%d capability=%s: %v",
					bi.ModelEntry.ModelName, bi.BotID, bi.OwnerID, bi.Capability, err)
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
