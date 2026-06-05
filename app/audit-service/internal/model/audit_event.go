package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

type AuditEvent struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	EventID        string    `gorm:"column:event_id;not null;uniqueIndex" json:"event_id"`
	Action         int32     `gorm:"column:action;not null" json:"action"`
	Result         int32     `gorm:"column:result;not null" json:"result"`
	Risk           int32     `gorm:"column:risk;not null" json:"risk"`
	UserID         int64     `gorm:"column:user_id;not null;index" json:"user_id"`
	DeviceID       string    `gorm:"column:device_id;not null;default:''" json:"device_id"`
	IPAddress      string    `gorm:"column:ip_address;not null;default:''" json:"ip_address"`
	UserAgent      string    `gorm:"column:user_agent;not null;default:''" json:"user_agent"`
	ResourceType   string    `gorm:"column:resource_type;not null;default:''" json:"resource_type"`
	ResourceID     string    `gorm:"column:resource_id;not null;default:''" json:"resource_id"`
	Detail         JSONMap   `gorm:"column:detail;type:jsonb;not null;default:'{}'" json:"detail"`
	ErrorMessage   string    `gorm:"column:error_message;not null;default:''" json:"error_message"`
	TraceID        string    `gorm:"column:trace_id;not null;default:''" json:"trace_id"`
	SpanID         string    `gorm:"column:span_id;not null;default:''" json:"span_id"`
	ServiceName    string    `gorm:"column:service_name;not null;default:''" json:"service_name"`
	ServiceVersion string    `gorm:"column:service_version;not null;default:''" json:"service_version"`
	ReviewStatus   int32     `gorm:"column:review_status;not null;default:0" json:"review_status"`
	ReviewResult   JSONMap   `gorm:"column:review_result;type:jsonb;not null;default:'{}'" json:"review_result"`
	IsArchived     bool      `gorm:"column:is_archived;not null;default:false" json:"is_archived"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"created_at"`
}

func (AuditEvent) TableName() string { return "audit_events" }

type JSONMap map[string]any

func (j JSONMap) Value() (driver.Value, error) {
	if j == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}
