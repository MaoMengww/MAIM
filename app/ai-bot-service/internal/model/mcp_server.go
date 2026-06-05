package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// McpServer mirrors the mcp_servers table for JOIN queries.
type McpServer struct {
	ID             int64              `gorm:"primaryKey;column:id"`
	Name           string             `gorm:"column:name"`
	Description    string             `gorm:"column:description"`
	Transport      string             `gorm:"column:transport"`
	URL            string             `gorm:"column:url"`
	Command        string             `gorm:"column:command"`
	AuthConfig     *MCPAuthConfig     `gorm:"column:auth_config;type:jsonb"`
	AdvancedConfig *MCPAdvancedConfig `gorm:"column:advanced_config;type:jsonb"`
	Enabled        bool               `gorm:"column:enabled;default:true"`
	CreatedBy      int64              `gorm:"column:created_by"`
	CreatedAt      time.Time          `gorm:"column:created_at"`
	UpdatedAt      time.Time          `gorm:"column:updated_at"`
}

func (McpServer) TableName() string { return "mcp_servers" }

type MCPAuthConfig struct {
	APIKey string `json:"api_key,omitempty"`
	Token  string `json:"token,omitempty"`
}

type MCPAdvancedConfig struct {
	Timeout    int `json:"timeout"`
	RetryCount int `json:"retry_count"`
	RetryDelay int `json:"retry_delay"`
}

func (c *MCPAuthConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

func (c *MCPAuthConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

func (c *MCPAdvancedConfig) Value() (driver.Value, error) {
	if c == nil {
		return nil, nil
	}
	return json.Marshal(c)
}

func (c *MCPAdvancedConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// BotMcpServer mirrors the bot_mcp_servers association table.
type BotMcpServer struct {
	ID             int64     `gorm:"primaryKey;column:id"`
	BotID          int64     `gorm:"column:bot_id"`
	McpServerID    int64     `gorm:"column:mcp_server_id"`
	Enabled        bool      `gorm:"column:enabled;default:true"`
	ConfigOverride []byte    `gorm:"column:config_override;type:jsonb"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

func (BotMcpServer) TableName() string { return "bot_mcp_servers" }
