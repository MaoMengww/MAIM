package model

import "time"

// McpTool represents a tool discovered from an MCP server.
type McpTool struct {
	ID          string    `gorm:"primaryKey;column:id;type:uuid"`
	McpServerID string    `gorm:"column:mcp_server_id;type:uuid"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	InputSchema string    `gorm:"column:input_schema;type:text"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (McpTool) TableName() string { return "mcp_tools" }
