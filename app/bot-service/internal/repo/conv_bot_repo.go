package repo

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

// ConvBotRepo queries the conv_bots table.
type ConvBotRepo struct {
	db *database.DB
}

func NewConvBotRepo(db *database.DB) *ConvBotRepo {
	return &ConvBotRepo{db: db}
}

// FindByBotAndConv returns the active binding for a bot in a conversation.
func (r *ConvBotRepo) FindByBotAndConv(ctx context.Context, botID, convID string) (*model.ConvBot, error) {
	var cb model.ConvBot
	err := r.db.WithContext(ctx).Where("bot_id = ? AND conv_id = ? AND revoked_at IS NULL", botID, convID).First(&cb).Error
	if err != nil {
		return nil, err
	}
	return &cb, nil
}

// FindByConv returns active bot bindings for a conversation.
func (r *ConvBotRepo) FindByConv(ctx context.Context, convID string) ([]model.ConvBot, error) {
	var bots []model.ConvBot
	err := r.db.WithContext(ctx).Where("conv_id = ? AND revoked_at IS NULL", convID).Find(&bots).Error
	return bots, err
}
