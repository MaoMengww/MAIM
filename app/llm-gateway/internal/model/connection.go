package model

import "time"

// ProviderConnection owns provider configuration and write-only encrypted credentials.
type ProviderConnection struct {
	ID              string    `gorm:"primaryKey;column:id;type:uuid"`
	Name            string    `gorm:"column:name"`
	Provider        string    `gorm:"column:provider"`
	BaseURL         string    `gorm:"column:base_url"`
	APIKeyEncrypted string    `gorm:"column:api_key_encrypted" json:"-"`
	QuotaPoolID     string    `gorm:"column:quota_pool_id;type:uuid"`
	OwnerType       string    `gorm:"column:owner_type"`
	OwnerID         *string   `gorm:"column:owner_id;type:uuid"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt       time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (ProviderConnection) TableName() string { return "provider_connections" }

type QuotaPool struct {
	ID                string    `gorm:"primaryKey;column:id;type:uuid"`
	Name              string    `gorm:"column:name"`
	OwnerType         string    `gorm:"column:owner_type"`
	OwnerID           *string   `gorm:"column:owner_id;type:uuid"`
	RequestsPerMinute int       `gorm:"column:requests_per_minute"`
	RequestsPerSecond int       `gorm:"column:requests_per_second"`
	TokensPerSecond   int       `gorm:"column:tokens_per_second"`
	Concurrency       int       `gorm:"column:concurrency"`
	CreatedAt         time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt         time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (QuotaPool) TableName() string { return "quota_pools" }
