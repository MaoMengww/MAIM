package domain

import (
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/pkg/identity"
)

type BillingRecorder interface {
	Record(record *model.BillingRecord) error
}

// NewBillingRecord allocates an independent usage identity in the LLM domain.
// OwnerID identifies the charged account, not the selected model's owner.
func NewBillingRecord(entry *ModelEntry, botID, ownerID *string, capability string, usage *UsageInfo) (*model.BillingRecord, error) {
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	ownerType := "platform"
	if ownerID != nil {
		ownerType = "user"
	}
	return &model.BillingRecord{
		ID: id, BotID: botID, OwnerType: ownerType, OwnerID: ownerID,
		ModelID: entry.ID, ModelName: entry.ModelName, Capability: capability,
		InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens,
		InputCost:  float64(usage.PromptTokens) / 1_000_000 * entry.InputPricePerMTok,
		OutputCost: float64(usage.CompletionTokens) / 1_000_000 * entry.OutputPricePerMTok,
		Provider:   entry.Provider, CreatedAt: time.Now(),
	}, nil
}
