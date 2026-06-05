package domain

type ModelEntry struct {
	ID                 int64
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
	OwnerID            int64
	Metadata           map[string]any
}

type ModelRegistry interface {
	FindByID(modelID int64) (*ModelEntry, error)
	FindByName(modelName string) (*ModelEntry, error)
	FindByCapability(capability string) ([]*ModelEntry, error)
	ListAll() []*ModelEntry
	Refresh() error
	Close()
}
