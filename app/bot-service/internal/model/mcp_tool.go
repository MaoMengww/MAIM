package model

import "time"

// McpTool represents a tool discovered from an MCP server.
type McpTool struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	McpServerID int64     `gorm:"column:mcp_server_id;index"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	InputSchema string    `gorm:"column:input_schema;type:text"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (McpTool) TableName() string { return "mcp_tools" }
