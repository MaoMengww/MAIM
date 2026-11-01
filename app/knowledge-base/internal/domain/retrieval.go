package domain

import (
	"fmt"
	"math"

	"github.com/maomeng/aim/pkg/knowledgecontract"
)

// EffectiveRetrievalConfig applies the same defaults before comparison and search.
// The request limit bounds output. Candidate recall covers that boundary, while
// the configured rerank top_n may deliberately return fewer results.
func EffectiveRetrievalConfig(cfg RetrievalConfig, limit int) (RetrievalConfig, error) {
	if err := (PipelineConfig{Retrieval: cfg}).ValidateModelReferences(); err != nil {
		return RetrievalConfig{}, err
	}
	if limit <= 0 {
		limit = knowledgecontract.DefaultSearchLimit
	}
	if cfg.Mode == "" {
		cfg.Mode = "hybrid"
	}
	switch cfg.Mode {
	case "vector", "fulltext", "hybrid":
	default:
		return RetrievalConfig{}, fmt.Errorf("invalid retrieval mode")
	}
	if cfg.CandidateTopK <= 0 {
		cfg.CandidateTopK = 20
	}
	cfg.CandidateTopK = max(cfg.CandidateTopK, limit)
	if math.IsNaN(float64(cfg.ScoreThreshold)) || math.IsInf(float64(cfg.ScoreThreshold), 0) {
		return RetrievalConfig{}, fmt.Errorf("invalid score threshold")
	}
	if cfg.Mode == "hybrid" {
		if cfg.DenseWeight == 0 && cfg.SparseWeight == 0 {
			cfg.DenseWeight, cfg.SparseWeight = 0.7, 0.3
		}
		if cfg.DenseWeight < 0 || cfg.SparseWeight < 0 || math.IsNaN(float64(cfg.DenseWeight)) || math.IsNaN(float64(cfg.SparseWeight)) || math.IsInf(float64(cfg.DenseWeight), 0) || math.IsInf(float64(cfg.SparseWeight), 0) {
			return RetrievalConfig{}, fmt.Errorf("invalid hybrid weights")
		}
	} else {
		cfg.DenseWeight, cfg.SparseWeight = 0, 0
	}
	if cfg.Rerank.Enabled {
		cfg.Rerank.Method = cfg.Rerank.EffectiveMethod()
		if cfg.Rerank.ModelID == nil {
			return RetrievalConfig{}, fmt.Errorf("rerank model is not configured")
		}
		if cfg.Rerank.TopN <= 0 {
			cfg.Rerank.TopN = 20
		}
	} else {
		cfg.Rerank = RerankConfig{}
	}
	return cfg, nil
}
