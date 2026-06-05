package repo

import (
	"context"
	"strconv"

	"github.com/maomeng/aim/app/bot-platform/internal/model"

	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/snowflake"
	"gorm.io/gorm"
)

type BotRepoInterface interface {
	CreateBot(ctx context.Context, bot *model.Bot) error
	UpdateBot(ctx context.Context, botID int64, updates map[string]any) error
	DeleteBot(ctx context.Context, botID int64) error
	GetBot(ctx context.Context, botID int64) (*model.Bot, error)
	GetBotsByIDs(ctx context.Context, botIDs []int64) ([]model.Bot, error)
	ListBotsByOwner(ctx context.Context, ownerID int64, status string, offset, limit int) ([]model.Bot, int64, error)
	ListOfficialTemplates(ctx context.Context) ([]model.Bot, error)
	GetOfficialInstance(ctx context.Context, ownerID int64, templateID int64) (*model.Bot, error)
	ListActiveWebhookBots(ctx context.Context) ([]WebhookBotSecret, error)
	NextID(ctx context.Context) (int64, error)

	CreateMcpServer(ctx context.Context, srv *model.McpServer) error
	UpdateMcpServer(ctx context.Context, id int64, updates map[string]any) error
	DeleteMcpServer(ctx context.Context, id int64) error
	GetMcpServer(ctx context.Context, id int64) (*model.McpServer, error)
	ListMcpServers(ctx context.Context, status string, offset, limit int) ([]model.McpServer, int64, error)
	ListUserMcpServers(ctx context.Context, userID int64, status string, offset, limit int) ([]model.McpServer, int64, error)

	AssignMcpToBot(ctx context.Context, assoc *model.BotMcpServer) error
	UnassignMcpFromBot(ctx context.Context, botID, mcpServerID int64) error
	ListBotMcpServers(ctx context.Context, botID int64) ([]model.BotMcpServer, error)
	UpdateBotMcpServer(ctx context.Context, botID, mcpServerID int64, enabled bool) error
	GetBotMcpServer(ctx context.Context, botID, mcpServerID int64) (*model.BotMcpServer, error)
	GetModelOwner(ctx context.Context, modelID int64) (int64, error)
	ResolveModelID(ctx context.Context, modelName string) (int64, error)

	// MCP Tool Discovery
	UpsertMcpTools(ctx context.Context, serverID int64, tools []model.McpTool) error
	ListMcpTools(ctx context.Context, serverID int64) ([]model.McpTool, error)
}

type BotRepo struct {
	DB        *database.DB
	Snowflake *snowflake.Node
}

func NewBotRepo(db *database.DB, sf *snowflake.Node) *BotRepo {
	return &BotRepo{DB: db, Snowflake: sf}
}

func (r *BotRepo) CreateBot(ctx context.Context, bot *model.Bot) error {
	return r.DB.WithContext(ctx).Create(bot).Error
}

func (r *BotRepo) UpdateBot(ctx context.Context, botID int64, updates map[string]any) error {
	return r.DB.WithContext(ctx).Model(&model.Bot{}).Where("id = ?", botID).Updates(updates).Error
}

func (r *BotRepo) DeleteBot(ctx context.Context, botID int64) error {
	return r.DB.WithContext(ctx).Where("id = ?", botID).Delete(&model.Bot{}).Error
}

func (r *BotRepo) GetBot(ctx context.Context, botID int64) (*model.Bot, error) {
	var bot model.Bot
	err := r.DB.WithContext(ctx).Where("id = ?", botID).First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *BotRepo) GetBotsByIDs(ctx context.Context, botIDs []int64) ([]model.Bot, error) {
	if len(botIDs) == 0 {
		return nil, nil
	}
	var bots []model.Bot
	err := r.DB.WithContext(ctx).Where("id IN ?", botIDs).Find(&bots).Error
	return bots, err
}

func (r *BotRepo) ListBotsByOwner(ctx context.Context, ownerID int64, status string, offset, limit int) ([]model.Bot, int64, error) {
	var bots []model.Bot
	var total int64

	q := r.DB.WithContext(ctx).Model(&model.Bot{}).Where("owner_id = ?", ownerID)
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
		Where("owner_id = 0").
		Find(&bots).Error
	return bots, err
}

func (r *BotRepo) GetOfficialInstance(ctx context.Context, ownerID int64, templateID int64) (*model.Bot, error) {
	var bot model.Bot
	err := r.DB.WithContext(ctx).
		Where("owner_id = ? AND settings->>'official_template_id' = ?", ownerID, strconv.FormatInt(templateID, 10)).
		First(&bot).Error
	if err != nil {
		return nil, err
	}
	return &bot, nil
}

func (r *BotRepo) NextID(ctx context.Context) (int64, error) {
	if r.Snowflake == nil {
		return 0, gorm.ErrInvalidDB
	}
	return r.Snowflake.GenerateWithError()
}

// ========== Global MCP Server CRUD ==========

func (r *BotRepo) CreateMcpServer(ctx context.Context, srv *model.McpServer) error {
	return r.DB.WithContext(ctx).Create(srv).Error
}

func (r *BotRepo) UpdateMcpServer(ctx context.Context, id int64, updates map[string]any) error {
	return r.DB.WithContext(ctx).Model(&model.McpServer{}).Where("id = ?", id).Updates(updates).Error
}

func (r *BotRepo) DeleteMcpServer(ctx context.Context, id int64) error {
	return r.DB.WithContext(ctx).Where("id = ?", id).Delete(&model.McpServer{}).Error
}

func (r *BotRepo) GetMcpServer(ctx context.Context, id int64) (*model.McpServer, error) {
	var srv model.McpServer
	err := r.DB.WithContext(ctx).Where("id = ?", id).First(&srv).Error
	if err != nil {
		return nil, err
	}
	return &srv, nil
}

func (r *BotRepo) ListMcpServers(ctx context.Context, status string, offset, limit int) ([]model.McpServer, int64, error) {
	var servers []model.McpServer
	var total int64

	q := r.DB.WithContext(ctx).Model(&model.McpServer{})
	if status != "" {
		q = q.Where("status = ?", status)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&servers).Error; err != nil {
		return nil, 0, err
	}

	return servers, total, nil
}

func (r *BotRepo) ListUserMcpServers(ctx context.Context, userID int64, status string, offset, limit int) ([]model.McpServer, int64, error) {
	var servers []model.McpServer
	var total int64

	q := r.DB.WithContext(ctx).Model(&model.McpServer{}).
		Where("created_by = ? OR created_by = 0", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&servers).Error; err != nil {
		return nil, 0, err
	}

	return servers, total, nil
}

// ========== Bot-MCP Assignment ==========

func (r *BotRepo) AssignMcpToBot(ctx context.Context, assoc *model.BotMcpServer) error {
	return r.DB.WithContext(ctx).Create(assoc).Error
}

func (r *BotRepo) UnassignMcpFromBot(ctx context.Context, botID, mcpServerID int64) error {
	return r.DB.WithContext(ctx).Where("bot_id = ? AND mcp_server_id = ?", botID, mcpServerID).
		Delete(&model.BotMcpServer{}).Error
}

func (r *BotRepo) ListBotMcpServers(ctx context.Context, botID int64) ([]model.BotMcpServer, error) {
	var assocs []model.BotMcpServer
	err := r.DB.WithContext(ctx).Where("bot_id = ?", botID).Find(&assocs).Error
	return assocs, err
}

func (r *BotRepo) UpdateBotMcpServer(ctx context.Context, botID, mcpServerID int64, enabled bool) error {
	return r.DB.WithContext(ctx).Model(&model.BotMcpServer{}).
		Where("bot_id = ? AND mcp_server_id = ?", botID, mcpServerID).
		Update("enabled", enabled).Error
}

func (r *BotRepo) GetBotMcpServer(ctx context.Context, botID, mcpServerID int64) (*model.BotMcpServer, error) {
	var assoc model.BotMcpServer
	err := r.DB.WithContext(ctx).Where("bot_id = ? AND mcp_server_id = ?", botID, mcpServerID).First(&assoc).Error
	if err != nil {
		return nil, err
	}
	return &assoc, nil
}

func (r *BotRepo) GetModelOwner(ctx context.Context, modelID int64) (int64, error) {
	var ownerID int64
	err := r.DB.WithContext(ctx).
		Table("llm.model_registry").
		Where("id = ? AND status = 'active'", modelID).
		Select("owner_id").
		Take(&ownerID).Error
	if err != nil {
		return 0, err
	}
	return ownerID, nil
}

type WebhookBotSecret struct {
	ID            int64
	WebhookSecret string
}

func (r *BotRepo) ListActiveWebhookBots(ctx context.Context) ([]WebhookBotSecret, error) {
	var bots []WebhookBotSecret
	err := r.DB.WithContext(ctx).Model(&model.Bot{}).
		Where("type = ? AND sub_type = ? AND status = 'active' AND webhook_secret != ''", "third_party", "webhook").
		Select("id, webhook_secret").
		Find(&bots).Error
	return bots, err
}

func (r *BotRepo) ResolveModelID(ctx context.Context, modelName string) (int64, error) {
	var id int64
	err := r.DB.WithContext(ctx).
		Table("llm.model_registry").
		Where("model_name = ? AND status = 'active'", modelName).
		Select("id").
		Take(&id).Error
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ========== MCP Tool Discovery ==========

func (r *BotRepo) UpsertMcpTools(ctx context.Context, serverID int64, tools []model.McpTool) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("mcp_server_id = ?", serverID).Delete(&model.McpTool{}).Error; err != nil {
			return err
		}
		if len(tools) == 0 {
			return nil
		}
		return tx.CreateInBatches(tools, 50).Error
	})
}

func (r *BotRepo) ListMcpTools(ctx context.Context, serverID int64) ([]model.McpTool, error) {
	var tools []model.McpTool
	err := r.DB.WithContext(ctx).
		Where("mcp_server_id = ?", serverID).
		Order("name ASC").
		Find(&tools).Error
	return tools, err
}
