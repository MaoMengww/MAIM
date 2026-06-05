package domain

import "github.com/maomeng/aim/app/llm-gateway/internal/model"

type BillingRecorder interface {
	Record(record *model.BillingRecord) error
}
