package repo

import (
	"context"

	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/database"
)

type BotRepo struct {
	db          *database.DB
	botPlatform botplatform.BotPlatformClient
}

func NewBotRepo(db *database.DB, botPlatform botplatform.BotPlatformClient) *BotRepo {
	return &BotRepo{db: db, botPlatform: botPlatform}
}

// BatchGetBotNames returns bot_id → name for the given IDs via gRPC.
func (r *BotRepo) BatchGetBotNames(ctx context.Context, botIDs []int64) (map[int64]string, error) {
	if len(botIDs) == 0 {
		return nil, nil
	}
	if r.botPlatform != nil {
		resp, err := r.botPlatform.BatchGetBots(ctx, &botplatform.BatchGetBotsReq{BotIds: botIDs})
		if err == nil {
			result := make(map[int64]string, len(resp.Bots))
			for _, pb := range resp.Bots {
				result[pb.Id] = pb.Name
			}
			return result, nil
		}
	}
	// fallback: direct DB query
	var rows []struct {
		ID   int64
		Name string
	}
	if err := r.db.WithContext(ctx).Table("bots").Select("id, name").
		Where("id IN ?", botIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]string, len(rows))
	for _, row := range rows {
		result[row.ID] = row.Name
	}
	return result, nil
}

// GetBotInfo returns bot name and avatar via gRPC.
func (r *BotRepo) GetBotInfo(ctx context.Context, botID int64) (name, avatar string, err error) {
	if r.botPlatform != nil {
		pbBot, err := r.botPlatform.GetBot(ctx, &botplatform.GetBotReq{BotId: botID})
		if err == nil {
			return pbBot.Name, pbBot.Avatar, nil
		}
	}
	// fallback: direct DB query
	var row struct {
		Name   string
		Avatar string
	}
	if err := r.db.WithContext(ctx).Table("bots").Select("name, avatar").
		Where("id = ?", botID).Take(&row).Error; err != nil {
		return "", "", err
	}
	return row.Name, row.Avatar, nil
}
