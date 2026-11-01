package handler

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
)

type searchModel struct {
	ID         string
	Provider   string
	ModelName  string
	BaseURL    string
	Capability string
	Status     string
	OwnerType  string
	OwnerID    *string
	Metadata   map[string]any `gorm:"serializer:json"`
}

// Registry rows are callable offerings, not vector-space identities. Two
// authorized offerings can use the same real model without sharing an owner.
// Conversely, equal dimensions never make different models compatible.
type searchModelSpace struct {
	Provider  string
	ModelName string
	Endpoint  string
	Dimension int
}

type searchCompatibility struct {
	Embedding      searchModelSpace
	Mode           string
	CandidateTopK  int
	ScoreThreshold float32
	DenseWeight    float32
	SparseWeight   float32
	RerankEnabled  bool
	RerankMethod   string
	RerankModel    searchModelSpace
	RerankTopN     int
}

func (h *KnowledgeBaseHandler) searchModelSpace(ctx context.Context, id, capability string, kb *domain.KnowledgeBase, cache map[string]searchModel) (searchModelSpace, error) {
	entry, exists := cache[id]
	if !exists {
		if h.DB == nil {
			return searchModelSpace{}, errors.New(errors.CodeIncompatibleScope, "knowledge search model configuration unavailable")
		}
		err := h.DB.WithContext(ctx).Table("llm.model_registry AS m").
			Select("m.id, c.provider, m.model_name, c.base_url, m.capability, m.status, m.owner_type, m.owner_id, m.metadata").
			Joins("JOIN llm.provider_connections c ON c.id = m.connection_id AND c.owner_type = m.owner_type AND c.owner_id IS NOT DISTINCT FROM m.owner_id").
			Where("m.id = ?", id).Take(&entry).Error
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return searchModelSpace{}, errors.New(errors.CodeIncompatibleScope, "knowledge search model unavailable")
		}
		if err != nil {
			return searchModelSpace{}, errors.Wrap(errors.CodeDBError, "resolve knowledge search model failed", err)
		}
		cache[id] = entry
	}
	if entry.Status != "active" || entry.Capability != capability || entry.ModelName == "" {
		return searchModelSpace{}, errors.New(errors.CodeIncompatibleScope, "knowledge search model unavailable")
	}
	switch entry.OwnerType {
	case "platform":
		if entry.OwnerID != nil {
			return searchModelSpace{}, errors.New(errors.CodeIncompatibleScope, "knowledge search model unavailable")
		}
	case "user":
		if entry.OwnerID == nil || kb.OwnerID == nil || *entry.OwnerID != *kb.OwnerID {
			return searchModelSpace{}, errors.New(errors.CodeIncompatibleScope, "knowledge search model unavailable")
		}
	default:
		return searchModelSpace{}, errors.New(errors.CodeIncompatibleScope, "knowledge search model unavailable")
	}
	endpoint := strings.TrimRight(entry.BaseURL, "/")
	// The current embedding adapter uses the OpenAI default for all non-Cohere
	// providers with an omitted URL. Match that effective behavior here.
	if capability == "embed" && entry.Provider != "cohere" && endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	space := searchModelSpace{Provider: entry.Provider, ModelName: entry.ModelName, Endpoint: endpoint}
	if capability == "embed" {
		space.Dimension = 1536 // the existing shared index's default space size
		if d, ok := entry.Metadata["dimension"].(float64); ok && d > 0 {
			space.Dimension = int(d)
		}
	}
	return space, nil
}

// Validate retained references too: disabling does not authorize an otherwise
// invalid model, while an explicit clear can remove a stale reference.
func (h *KnowledgeBaseHandler) validateRerankModel(ctx context.Context, kb *domain.KnowledgeBase, cache map[string]searchModel) error {
	cfg := kb.PipelineConfig.Retrieval.Rerank
	if cfg.ModelID == nil {
		if cfg.Enabled {
			return errors.New(errors.CodeIncompatibleScope, "knowledge search rerank model is not configured")
		}
		return nil
	}
	_, err := h.searchModelSpace(ctx, *cfg.ModelID, cfg.EffectiveMethod(), kb, cache)
	return err
}

func (h *KnowledgeBaseHandler) resolveSearchConfiguration(ctx context.Context, kbs []*domain.KnowledgeBase, limit int) (domain.RetrievalConfig, string, *string, error) {
	var configured domain.RetrievalConfig
	var embeddingID string
	var ownerID *string
	var common searchCompatibility
	cache := make(map[string]searchModel, len(kbs))
	for i, kb := range kbs {
		cfg, err := domain.EffectiveRetrievalConfig(kb.PipelineConfig.Retrieval, limit)
		if err != nil {
			return domain.RetrievalConfig{}, "", nil, errors.New(errors.CodeIncompatibleScope, "knowledge search configuration unavailable")
		}
		if err := h.validateRerankModel(ctx, kb, cache); err != nil {
			return domain.RetrievalConfig{}, "", nil, err
		}
		modelID, err := kb.ResolveEmbeddingModelID(ctx, h.KBRepo)
		if err != nil {
			return domain.RetrievalConfig{}, "", nil, errors.New(errors.CodeIncompatibleScope, "knowledge search embedding model unavailable")
		}
		space, err := h.searchModelSpace(ctx, modelID, "embed", kb, cache)
		if err != nil {
			return domain.RetrievalConfig{}, "", nil, err
		}
		compatibility := searchCompatibility{
			Embedding: space, Mode: cfg.Mode, CandidateTopK: cfg.CandidateTopK,
			ScoreThreshold: cfg.ScoreThreshold, DenseWeight: cfg.DenseWeight, SparseWeight: cfg.SparseWeight,
			RerankEnabled: cfg.Rerank.Enabled, RerankTopN: cfg.Rerank.TopN,
		}
		if cfg.Rerank.Enabled {
			compatibility.RerankMethod = cfg.Rerank.Method
			compatibility.RerankModel, err = h.searchModelSpace(ctx, *cfg.Rerank.ModelID, cfg.Rerank.Method, kb, cache)
			if err != nil {
				return domain.RetrievalConfig{}, "", nil, err
			}
		}
		if i == 0 {
			common = compatibility
			configured, embeddingID, ownerID = cfg, modelID, kb.OwnerID
		} else if compatibility != common {
			return domain.RetrievalConfig{}, "", nil, errors.New(errors.CodeIncompatibleScope, "incompatible knowledge search scope")
		}
	}
	// Scope is sorted and every effective configuration was checked before an
	// offering is used. This deterministic invocation identity retains the
	// selected KB owner's existing model visibility and billing semantics.
	return configured, embeddingID, ownerID, nil
}
