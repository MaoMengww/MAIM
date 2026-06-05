package infra

import (
	"github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type BillingRepo struct {
	db *database.DB
}

func NewBillingRepo(db *database.DB) *BillingRepo {
	return &BillingRepo{db: db}
}

func (r *BillingRepo) Record(record *model.BillingRecord) error {
	return r.db.Create(record).Error
}
