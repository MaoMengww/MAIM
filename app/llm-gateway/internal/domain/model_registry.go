package domain

import "context"

type ModelEntry struct {
	ID                 string
	ModelName          string
	ConnectionID       string
	QuotaPoolID        string
	PoolLimits         CapacityLimits
	ModelLimits        CapacityLimits
	PoolRevision       int64
	ModelRevision      int64
	Provider           string
	Capability         string
	BaseURL            string
	APIKey             string
	ContextWindow      int
	MaxOutputTokens    int
	InputPricePerMTok  float64
	OutputPricePerMTok float64
	Status             string
	OwnerType          string
	OwnerID            *string
	Metadata           map[string]any
}

type ModelRegistry interface {
	FindByID(context.Context, string) (*ModelEntry, error)
	FindByName(modelName string) (*ModelEntry, error)
	FindByCapability(capability string, ownerID *string) ([]*ModelEntry, error)
	ListAll() []*ModelEntry
	Refresh() error
	Close()
}
