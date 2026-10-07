package repo

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BotRepoInterface interface {
	CreateBot(ctx context.Context, bot *model.Bot) error
	UpdateBot(ctx context.Context, botID string, updates map[string]any) error
	DeleteBot(ctx context.Context, botID string) error
	GetBot(ctx context.Context, botID string) (*model.Bot, error)
	GetBotsByIDs(ctx context.Context, botIDs []string) ([]model.Bot, error)
	ListBotsByOwner(ctx context.Context, ownerType string, ownerID *string, status string, offset, limit int) ([]model.Bot, int64, error)
	ListOfficialTemplates(ctx context.Context) ([]model.Bot, error)
	GetOfficialInstance(ctx context.Context, ownerID, templateID string) (*model.Bot, error)
	ListActiveWebhookBots(ctx context.Context) ([]WebhookBotSecret, error)

	CreateMcpServer(ctx context.Context, srv *model.McpServer) error
	UpdateMcpServer(ctx context.Context, id string, updates map[string]any) error
	DeleteMcpServer(ctx context.Context, id string) error
	GetMcpServer(ctx context.Context, id string) (*model.McpServer, error)
	ListMcpServers(ctx context.Context, status string, offset, limit int) ([]model.McpServer, int64, error)
	ListUserMcpServers(ctx context.Context, userID string, status string, offset, limit int) ([]model.McpServer, int64, error)

	AssignMcpToBot(ctx context.Context, assoc *model.BotMcpServer) error
	UnassignMcpFromBot(ctx context.Context, botID, mcpServerID string) error
	ListBotMcpServers(ctx context.Context, botID string) ([]model.BotMcpServer, error)
	UpdateBotMcpServer(ctx context.Context, botID, mcpServerID string, enabled bool) error
	GetBotMcpServer(ctx context.Context, botID, mcpServerID string) (*model.BotMcpServer, error)
	GetModelOwner(ctx context.Context, modelID string) (string, *string, error)
	ResolveModelID(ctx context.Context, modelName, userID string, usePlatformModel bool) (string, error)

	UpsertMcpTools(ctx context.Context, serverID string, tools []model.McpTool) error
	ListMcpTools(ctx context.Context, serverID string) ([]model.McpTool, error)
}

type BotRepo struct {
	DB *database.DB
}

func NewBotRepo(db *database.DB) *BotRepo {
	return &BotRepo{DB: db}
}

func (r *BotRepo) CreateBot(ctx context.Context, bot *model.Bot) error {
	return r.DB.WithContext(ctx).Create(bot).Error
}

func (r *BotRepo) UpdateBot(ctx context.Context, botID string, updates map[string]any) error {
	return r.DB.WithContext(ctx).Model(&model.Bot{}).Where("id = ?", botID).Updates(updates).Error
}

func (r *BotRepo) DeleteBot(ctx context.Context, botID string) error {
	return r.DB.WithContext(ctx).Where("id = ?", botID).Delete(&model.Bot{}).Error
}

func (r *BotRepo) GetBot(ctx context.Context, botID string) (*model.Bot, error) {
	var bot model.Bot
	if err := r.DB.WithContext(ctx).Where("id = ?", botID).First(&bot).Error; err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *BotRepo) GetBotsByIDs(ctx context.Context, botIDs []string) ([]model.Bot, error) {
	if len(botIDs) == 0 {
		return nil, nil
	}
	var bots []model.Bot
	err := r.DB.WithContext(ctx).Where("id IN ?", botIDs).Find(&bots).Error
	return bots, err
}

func (r *BotRepo) ListBotsByOwner(ctx context.Context, ownerType string, ownerID *string, status string, offset, limit int) ([]model.Bot, int64, error) {
	var bots []model.Bot
	var total int64
	q := r.DB.WithContext(ctx).Model(&model.Bot{}).Where("owner_type = ?", ownerType)
	if ownerID != nil {
		q = q.Where("owner_id = ?", *ownerID)
	} else {
		q = q.Where("owner_id IS NULL")
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&bots).Error; err != nil {
		return nil, 0, err
	}
	return bots, total, nil
}

func (r *BotRepo) ListOfficialTemplates(ctx context.Context) ([]model.Bot, error) {
	var bots []model.Bot
	err := r.DB.WithContext(ctx).Model(&model.Bot{}).
		Where("owner_type = ? AND owner_id IS NULL AND type = ?", "platform", "official").
		Find(&bots).Error
	return bots, err
}

func (r *BotRepo) GetOfficialInstance(ctx context.Context, ownerID, templateID string) (*model.Bot, error) {
	var bot model.Bot
	err := r.DB.WithContext(ctx).
		Where("owner_type = ? AND owner_id = ? AND settings->>'official_template_id' = ?", "user", ownerID, templateID).
		First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *BotRepo) CreateMcpServer(ctx context.Context, srv *model.McpServer) error {
	return r.DB.WithContext(ctx).Create(srv).Error
}

func (r *BotRepo) UpdateMcpServer(ctx context.Context, id string, updates map[string]any) error {
	return r.DB.WithContext(ctx).Model(&model.McpServer{}).Where("id = ?", id).Updates(updates).Error
}

func (r *BotRepo) DeleteMcpServer(ctx context.Context, id string) error {
	return r.DB.WithContext(ctx).Where("id = ?", id).Delete(&model.McpServer{}).Error
}

func (r *BotRepo) GetMcpServer(ctx context.Context, id string) (*model.McpServer, error) {
	var srv model.McpServer
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&srv).Error; err != nil {
		return nil, err
	}
	return &srv, nil
}

func (r *BotRepo) ListMcpServers(ctx context.Context, status string, offset, limit int) ([]model.McpServer, int64, error) {
	return r.listMcpServers(r.DB.WithContext(ctx).Model(&model.McpServer{}), status, offset, limit)
}

func (r *BotRepo) ListUserMcpServers(ctx context.Context, userID string, status string, offset, limit int) ([]model.McpServer, int64, error) {
	q := r.DB.WithContext(ctx).Model(&model.McpServer{}).
		Where("owner_type = ? OR (owner_type = ? AND owner_id = ?)", "platform", "user", userID)
	return r.listMcpServers(q, status, offset, limit)
}

func (r *BotRepo) listMcpServers(q *gorm.DB, status string, offset, limit int) ([]model.McpServer, int64, error) {
	var servers []model.McpServer
	var total int64
	switch status {
	case "active":
		q = q.Where("enabled = true")
	case "disabled":
		q = q.Where("enabled = false")
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&servers).Error; err != nil {
		return nil, 0, err
	}
	return servers, total, nil
}

func (r *BotRepo) AssignMcpToBot(ctx context.Context, assoc *model.BotMcpServer) error {
	return r.DB.WithContext(ctx).Create(assoc).Error
}

func (r *BotRepo) UnassignMcpFromBot(ctx context.Context, botID, mcpServerID string) error {
	return r.DB.WithContext(ctx).Where("bot_id = ? AND mcp_server_id = ?", botID, mcpServerID).
		Delete(&model.BotMcpServer{}).Error
}

func (r *BotRepo) ListBotMcpServers(ctx context.Context, botID string) ([]model.BotMcpServer, error) {
	var assocs []model.BotMcpServer
	err := r.DB.WithContext(ctx).Where("bot_id = ?", botID).Find(&assocs).Error
	return assocs, err
}

func (r *BotRepo) UpdateBotMcpServer(ctx context.Context, botID, mcpServerID string, enabled bool) error {
	result := r.DB.WithContext(ctx).Model(&model.BotMcpServer{}).
		Where("bot_id = ? AND mcp_server_id = ?", botID, mcpServerID).
		Update("enabled", enabled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *BotRepo) GetBotMcpServer(ctx context.Context, botID, mcpServerID string) (*model.BotMcpServer, error) {
	var assoc model.BotMcpServer
	if err := r.DB.WithContext(ctx).Where("bot_id = ? AND mcp_server_id = ?", botID, mcpServerID).First(&assoc).Error; err != nil {
		return nil, err
	}
	return &assoc, nil
}

func (r *BotRepo) GetModelOwner(ctx context.Context, modelID string) (string, *string, error) {
	var owner struct {
		OwnerType string
		OwnerID   *string
	}
	err := r.DB.WithContext(ctx).Table("llm.model_registry").
		Where("id = ? AND status = 'active'", modelID).
		Select("owner_type, owner_id").Take(&owner).Error
	return owner.OwnerType, owner.OwnerID, err
}

type WebhookBotSecret struct {
	ID            string
	WebhookSecret string
}

func (r *BotRepo) ListActiveWebhookBots(ctx context.Context) ([]WebhookBotSecret, error) {
	var bots []WebhookBotSecret
	err := r.DB.WithContext(ctx).Model(&model.Bot{}).
		Where("type = ? AND sub_type = ? AND status = 'active' AND webhook_secret != ''", "third_party", "webhook").
		Select("id, webhook_secret").Find(&bots).Error
	return bots, err
}

func (r *BotRepo) ResolveModelID(ctx context.Context, modelName, userID string, usePlatformModel bool) (string, error) {
	var id string
	q := r.DB.WithContext(ctx).Table("llm.model_registry").
		Where("model_name = ? AND status = 'active'", modelName)
	if usePlatformModel {
		q = q.Where("owner_type = ? AND owner_id IS NULL", "platform")
	} else {
		q = q.Where("owner_type = ? AND owner_id = ?", "user", userID)
	}
	err := q.Select("id").Order("id ASC").Take(&id).Error
	return id, err
}

// Rediscovery retains a tool's identity for the same server and external name.
func (r *BotRepo) UpsertMcpTools(ctx context.Context, serverID string, tools []model.McpTool) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var server model.McpServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", serverID).First(&server).Error; err != nil {
			return err
		}
		var existing []model.McpTool
		if err := tx.Where("mcp_server_id = ?", serverID).Find(&existing).Error; err != nil {
			return err
		}
		byName := make(map[string]model.McpTool, len(existing))
		for _, tool := range existing {
			byName[tool.Name] = tool
		}
		names := make([]string, 0, len(tools))
		seen := make(map[string]bool, len(tools))
		for i := range tools {
			tool := &tools[i]
			if tool.Name == "" || seen[tool.Name] {
				return fmt.Errorf("invalid or duplicate MCP tool name %q", tool.Name)
			}
			seen[tool.Name] = true
			names = append(names, tool.Name)
			tool.McpServerID = serverID
			if prior, ok := byName[tool.Name]; ok {
				tool.ID = prior.ID
				tool.CreatedAt = prior.CreatedAt
			} else {
				id, err := identity.New()
				if err != nil {
					return err
				}
				tool.ID = id
			}
		}
		stale := tx.Where("mcp_server_id = ?", serverID)
		if len(names) > 0 {
			stale = stale.Where("name NOT IN ?", names)
		}
		if err := stale.Delete(&model.McpTool{}).Error; err != nil {
			return err
		}
		if len(tools) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"description", "input_schema", "updated_at"}),
		}).CreateInBatches(tools, 50).Error
	})
}

func (r *BotRepo) ListMcpTools(ctx context.Context, serverID string) ([]model.McpTool, error) {
	var tools []model.McpTool
	err := r.DB.WithContext(ctx).Where("mcp_server_id = ?", serverID).Order("name ASC").Find(&tools).Error
	return tools, err
}

func (r *BotRepo) ListEnabledMcpServers(ctx context.Context, botID string) ([]model.McpServer, error) {
	var servers []model.McpServer
	err := r.DB.WithContext(ctx).Table("mcp_servers").
		Joins("INNER JOIN bot_mcp_servers ON bot_mcp_servers.mcp_server_id = mcp_servers.id").
		Where("bot_mcp_servers.bot_id = ? AND bot_mcp_servers.enabled = true AND mcp_servers.enabled = true", botID).
		Select("mcp_servers.*").Find(&servers).Error
	return servers, err
}

func (r *BotRepo) FindByIDWithConvBot(ctx context.Context, botID, convID string) (*model.Bot, *model.ConvBot, error) {
	bot, err := r.GetBot(ctx, botID)
	if err != nil {
		return nil, nil, err
	}
	var cb model.ConvBot
	if err := r.DB.WithContext(ctx).Where("bot_id = ? AND conv_id = ?", botID, convID).First(&cb).Error; err != nil {
		return bot, nil, err
	}
	return bot, &cb, nil
}
