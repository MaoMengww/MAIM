package domain

type ModelEntry struct {
	ID                 string
	ModelName          string
	Provider           string
	Capability         string
	BaseURL            string
	APIKeyEncrypted    string
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
	FindByID(modelID string) (*ModelEntry, error)
	FindByName(modelName string) (*ModelEntry, error)
	FindByCapability(capability string, ownerID *string) ([]*ModelEntry, error)
	ListAll() []*ModelEntry
	Refresh() error
	Close()
}
