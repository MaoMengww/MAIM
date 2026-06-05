package repo

import (
	"context"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

// ConvBotRepo queries the conv_bots table.
type ConvBotRepo struct {
	db *database.DB
}

func NewConvBotRepo(db *database.DB) *ConvBotRepo {
	return &ConvBotRepo{db: db}
}

// FindByBotAndConv returns a ConvBot entry.
func (r *ConvBotRepo) FindByBotAndConv(ctx context.Context, botID, convID int64) (*model.ConvBot, error) {
	var cb model.ConvBot
	err := r.db.WithContext(ctx).Where("bot_id = ? AND conv_id = ?", botID, convID).First(&cb).Error
	if err != nil {
		return nil, err
	}
	return &cb, nil
}

// FindByConv returns ConvBot entries for a conversation (may be multiple bots).
func (r *ConvBotRepo) FindByConv(ctx context.Context, convID int64) ([]model.ConvBot, error) {
	var bots []model.ConvBot
	err := r.db.WithContext(ctx).Where("conv_id = ?", convID).Find(&bots).Error
	return bots, err
}
