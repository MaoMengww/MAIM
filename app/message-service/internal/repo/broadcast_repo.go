package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type BroadcastRepo struct {
	db *database.DB
}

func NewBroadcastRepo(db *database.DB) *BroadcastRepo {
	return &BroadcastRepo{db: db}
}

func (r *BroadcastRepo) Insert(ctx context.Context, broadcast *model.Broadcast) error {
	return r.db.WithContext(ctx).Create(broadcast).Error
}
