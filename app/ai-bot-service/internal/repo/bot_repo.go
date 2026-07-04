package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/database"
)

// BotRepo provides Bot data via bot-platform gRPC, with DB fallback.
type BotRepo struct {
	db          *database.DB
	botPlatform botplatform.BotPlatformClient
}

func NewBotRepo(db *database.DB, botPlatform botplatform.BotPlatformClient) *BotRepo {
	return &BotRepo{db: db, botPlatform: botPlatform}
}

// FindByID returns a Bot by its ID via gRPC (with DB fallback).
func (r *BotRepo) FindByID(ctx context.Context, botID int64) (*model.Bot, error) {
	if r.botPlatform != nil {
		pbBot, err := r.botPlatform.GetBot(ctx, &botplatform.GetBotReq{BotId: botID})
		if err == nil {
			return protoToBot(pbBot), nil
		}
	}
	// fallback: direct DB query
	var bot model.Bot
	err := r.db.WithContext(ctx).Where("id = ?", botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

// FindByIDs returns multiple bots by their IDs via gRPC.
func (r *BotRepo) FindByIDs(ctx context.Context, botIDs []int64) ([]*model.Bot, error) {
	if len(botIDs) == 0 {
		return nil, nil
	}
	if r.botPlatform != nil {
		resp, err := r.botPlatform.BatchGetBots(ctx, &botplatform.BatchGetBotsReq{BotIds: botIDs})
		if err == nil {
			bots := make([]*model.Bot, len(resp.Bots))
			for i, pb := range resp.Bots {
				bots[i] = protoToBot(pb)
			}
			return bots, nil
		}
	}
	// fallback: direct DB query
	var bots []*model.Bot
	err := r.db.WithContext(ctx).Where("id IN ?", botIDs).Find(&bots).Error
	return bots, err
}

// ListBotMcpServers returns all enabled MCP servers assigned to a bot.
func (r *BotRepo) ListBotMcpServers(ctx context.Context, botID int64) ([]model.McpServer, error) {
	var servers []model.McpServer
	err := r.db.WithContext(ctx).
		Table("mcp_servers").
		Joins("INNER JOIN bot_mcp_servers ON bot_mcp_servers.mcp_server_id = mcp_servers.id").
		Where("bot_mcp_servers.bot_id = ? AND bot_mcp_servers.enabled = true AND mcp_servers.enabled = true", botID).
		Find(&servers).Error
	return servers, err
}

func (r *BotRepo) FindByIDWithConvBot(ctx context.Context, botID, convID int64) (*model.Bot, *model.ConvBot, error) {
	bot, err := r.FindByID(ctx, botID)
	if err != nil {
		return nil, nil, err
	}
	var cb model.ConvBot
	if err := r.db.WithContext(ctx).Where("bot_id = ? AND conv_id = ?", botID, convID).First(&cb).Error; err != nil {
		return bot, nil, err
	}
	return bot, &cb, nil
}

// protoToBot converts a proto Bot to the local model.Bot.
func protoToBot(pb *botplatform.Bot) *model.Bot {
	if pb == nil {
		return nil
	}
	bot := &model.Bot{
		ID:                       pb.Id,
		OwnerID:                  pb.OwnerId,
		Name:                     pb.Name,
		Avatar:                   pb.Avatar,
		Type:                     pb.Type,
		Status:                   pb.Status,
		ModelID:                  pb.ModelId,
		UsePlatformModel:         pb.UsePlatformModel,
		ModelName:                pb.ModelName,
		BaseURL:                  pb.BaseUrl,
		SystemPrompt:             pb.SystemPrompt,
		Persona:                  pb.Persona,
		EnableKnowledge:          pb.EnableKnowledge,
		Temperature:              pb.Temperature,
		MaxContextMessages:       int(pb.MaxContextMessages),
		StreamingEnabled:         pb.StreamingEnabled,
		MemoryModelName:          pb.MemoryModelName,
		MemoryModelID:            pb.MemoryModelId,
		MemoryUsePlatformModel:   pb.MemoryUsePlatformModel,
		MemoryLimit:              int(pb.MemoryLimit),
		MemoryEmbeddingModelName: pb.MemoryEmbeddingModelName,
		MemoryEmbeddingModelID:   pb.MemoryEmbeddingModelId,
		ConnMode:                 pb.ConnMode,
		CallbackURL:              pb.CallbackUrl,
		BotTags:                  pb.BotTags,
		ResponseTriggers:         pb.ResponseTriggers,
	}
	if pb.CreatedAt > 0 {
		bot.CreatedAt = time.Unix(pb.CreatedAt, 0)
	}
	if pb.UpdatedAt > 0 {
		bot.UpdatedAt = time.Unix(pb.UpdatedAt, 0)
	}
	if pb.Capabilities != "" {
		var caps model.Capabilities
		if err := json.Unmarshal([]byte(pb.Capabilities), &caps); err == nil {
			bot.Capabilities = caps
		}
	}

	// Parse settings JSON string into map
	if pb.Settings != "" {
		var settings map[string]any
		if err := json.Unmarshal([]byte(pb.Settings), &settings); err == nil {
			bot.Settings = settings
		}
	}

	return bot
}
