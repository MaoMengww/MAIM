package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// Bot mirrors the bots table.
type Bot struct {
	ID                     int64          `gorm:"primaryKey;column:id" json:"id"`
	OwnerID                int64          `gorm:"column:owner_id" json:"owner_id"`
	Name                   string         `gorm:"column:name" json:"name"`
	Avatar                 string         `gorm:"column:avatar" json:"avatar"`
	Type                   string         `gorm:"column:type" json:"type"`
	Status                 string         `gorm:"column:status" json:"status"`
	ModelID                int64          `gorm:"column:model_id" json:"model_id"`
	UsePlatformModel       bool           `gorm:"column:use_platform_model" json:"use_platform_model"`
	ModelName              string         `gorm:"column:model_name" json:"model_name"`
	BaseURL                string         `gorm:"column:base_url" json:"base_url"`
	APIKeyEncrypted        string         `gorm:"column:api_key_encrypted" json:"-"`
	SystemPrompt           string         `gorm:"column:system_prompt" json:"system_prompt"`
	Persona                string         `gorm:"column:persona" json:"persona"`
	EnableKnowledge        bool           `gorm:"column:enable_knowledge" json:"enable_knowledge"`
	Temperature            float64        `gorm:"column:temperature" json:"temperature"`
	MaxContextMessages     int            `gorm:"column:max_context_messages" json:"max_context_messages"`
	MaxStep                int            `gorm:"column:max_step;default:5" json:"max_step"`
	StreamingEnabled       bool           `gorm:"column:streaming_enabled" json:"streaming_enabled"`
	MemoryModelName        string         `gorm:"column:memory_model_name" json:"memory_model_name"`
	MemoryModelID          int64          `gorm:"column:memory_model_id" json:"memory_model_id"`
	MemoryUsePlatformModel bool           `gorm:"column:memory_use_platform_model" json:"memory_use_platform_model"`
	MemoryLimit            int            `gorm:"column:memory_limit;default:0" json:"memory_limit"`
	MemoryAPIKeyEncrypted  string         `gorm:"column:memory_api_key_encrypted" json:"-"`
	ConnMode               string         `gorm:"column:conn_mode" json:"conn_mode"`
	WebhookSecret          string         `gorm:"column:webhook_secret" json:"-"`
	CallbackURL            string         `gorm:"column:callback_url" json:"callback_url"`
	AppSecretHash          string         `gorm:"column:app_secret_hash" json:"-"`
	BotTags                StringArray    `gorm:"column:bot_tags;serializer:json" json:"bot_tags"`
	Capabilities           Capabilities   `gorm:"column:capabilities;serializer:json" json:"capabilities"`
	Settings               map[string]any `gorm:"column:settings;serializer:json" json:"settings"`
	CreatedAt              time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt              time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Bot) TableName() string { return "bots" }

// Capabilities is the JSONB field for bots.capabilities.
type Capabilities struct {
	MCPServers   []MCPServerConfig `json:"mcp_servers,omitempty"`
	BuiltinTools []string          `json:"builtin_tools,omitempty"` // built-in tool names: "web_search"
}

func (c Capabilities) Value() (driver.Value, error) {
	return json.Marshal(c)
}

func (c *Capabilities) Scan(value any) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(b, c)
}

// MCPServerConfig represents one MCP server entry in capabilities.
type MCPServerConfig struct {
	Name           string             `json:"name"`
	Transport      string             `json:"transport"`
	URL            string             `json:"url"`
	AuthConfig     *MCPAuthConfig     `json:"auth_config,omitempty"`
	AdvancedConfig *MCPAdvancedConfig `json:"advanced_config,omitempty"`
	TimeoutMs      int                `json:"timeout_ms"`
}

// StringArray is a PostgreSQL text array.
type StringArray []string

func (a StringArray) Value() (driver.Value, error) {
	return json.Marshal(a)
}

func (a *StringArray) Scan(value any) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(b, a)
}
