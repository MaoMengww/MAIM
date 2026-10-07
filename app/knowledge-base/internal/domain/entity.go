package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maomeng/aim/pkg/identity"
)

type KnowledgeBase struct {
	ID                string         `json:"id" gorm:"type:uuid;primaryKey"`
	OwnerType         string         `json:"owner_type" gorm:"not null"`
	OwnerID           *string        `json:"owner_id,omitempty" gorm:"type:uuid"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	EmbeddingModel    string         `json:"embedding_model"`
	EmbeddingModelID  *string        `json:"embedding_model_id,omitempty" gorm:"type:uuid"`
	Mode              string         `json:"mode" gorm:"type:varchar(16);not null;default:'rag'"`
	PipelineConfig    PipelineConfig `json:"pipeline_config" gorm:"type:jsonb;serializer:json"`
	DocCount          int            `json:"doc_count"`
	TotalChunks       int            `json:"total_chunks"`
	Status            string         `json:"status"`
	LastMaintenanceAt *time.Time     `json:"last_maintenance_at"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func (KnowledgeBase) TableName() string {
	return "knowledge_bases"
}

func (kb KnowledgeBase) ValidateOwner() error {
	switch kb.OwnerType {
	case "platform":
		if kb.OwnerID != nil {
			return fmt.Errorf("platform knowledge base must not have an owner_id")
		}
	case "user":
		if kb.OwnerID == nil {
			return fmt.Errorf("user knowledge base requires owner_id")
		}
		return identity.Validate(*kb.OwnerID)
	default:
		return fmt.Errorf("owner_type must be platform or user")
	}
	return nil
}

func (kb KnowledgeBase) ResolveEmbeddingModelID(ctx context.Context, repo KBRepo) (string, error) {
	if err := kb.ValidateOwner(); err != nil {
		return "", err
	}
	if kb.EmbeddingModelID != nil {
		if err := identity.Validate(*kb.EmbeddingModelID); err != nil {
			return "", err
		}
		return *kb.EmbeddingModelID, nil
	}
	if kb.EmbeddingModel == "" {
		return "", fmt.Errorf("embedding model is not configured")
	}
	return repo.ResolveModelID(ctx, kb.EmbeddingModel)
}

type DocStatus string

const (
	DocStatusPending   DocStatus = "pending"
	DocStatusParsing   DocStatus = "parsing"
	DocStatusChunking  DocStatus = "chunking"
	DocStatusEmbedding DocStatus = "embedding"
	DocStatusReady     DocStatus = "ready"
	DocStatusFailed    DocStatus = "failed"
)

type Document struct {
	ID               string          `json:"id" gorm:"type:uuid;primaryKey"`
	KBID             string          `json:"kb_id" gorm:"type:uuid;not null"`
	Title            string          `json:"title"`
	FileType         string          `json:"file_type"`
	FileSize         int64           `json:"file_size"`
	OriginalFilename string          `json:"original_filename"`
	MinioBucket      string          `json:"minio_bucket"`
	MinioKey         string          `json:"minio_key"`
	ContentHash      string          `json:"content_hash"`
	Status           DocStatus       `json:"status"`
	Stages           []Stage         `json:"stages" gorm:"type:jsonb;serializer:json"`
	ChunkCount       int             `json:"chunk_count"`
	ErrorMessage     string          `json:"error_message"`
	PipelineOverride *PipelineConfig `json:"pipeline_override" gorm:"type:jsonb;serializer:json"`
	Metadata         map[string]any  `json:"metadata" gorm:"type:jsonb;serializer:json"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

func (Document) TableName() string {
	return "documents"
}

type ChunkRecord struct {
	ID            string         `json:"id" gorm:"type:uuid;primaryKey"`
	DocID         string         `json:"doc_id" gorm:"type:uuid;not null"`
	KBID          string         `json:"kb_id" gorm:"type:uuid;not null"`
	ParentChunkID *string        `json:"parent_chunk_id" gorm:"type:uuid;index"`
	ChunkIndex    int            `json:"chunk_index"`
	Content       string         `json:"content"`
	TokenCount    int            `json:"token_count"`
	Metadata      map[string]any `json:"metadata" gorm:"type:jsonb;serializer:json"`
	CreatedAt     time.Time      `json:"created_at"`
}

func (ChunkRecord) TableName() string {
	return "document_chunks"
}

type KnowledgeBinding struct {
	ID         string    `json:"id" gorm:"type:uuid;primaryKey"`
	KBID       string    `json:"kb_id" gorm:"type:uuid;not null"`
	KBName     string    `json:"kb_name" gorm:"<-:false"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id" gorm:"type:uuid;not null"`
	CreatedAt  time.Time `json:"created_at"`
}

func (KnowledgeBinding) TableName() string {
	return "knowledge_bindings"
}

type PipelineConfig struct {
	Parsing   ParsingConfig   `json:"parsing"`
	Chunking  ChunkingConfig  `json:"chunking"`
	Retrieval RetrievalConfig `json:"retrieval"`
}

type ParsingConfig struct {
	// Engines 是解析引擎优先级列表，按顺序尝试第一个支持该文件类型的引擎
	// 可选值: "builtin", "mineru_precision", "mineru_agent"
	Engines []string `json:"engines"`
	// MinerUPrecision 精准解析 API 配置（需 Token）
	MinerUPrecision *MinerUConfig `json:"mineru_precision,omitempty"`
	// MinerUAgent 轻量解析 API 配置（免登录）
	MinerUAgent *MinerUConfig `json:"mineru_agent,omitempty"`
	// VLM 图片解析配置
	VLM *VLMConfig `json:"vlm,omitempty"`
}

type MinerUConfig struct {
	APIURL   string `json:"api_url,omitempty"`
	APIToken string `json:"api_token,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

type VLMConfig struct {
	ModelID  *string `json:"model_id,omitempty"`
	Enabled  bool    `json:"enabled"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	APIKey   string  `json:"api_key"`
	BaseURL  string  `json:"base_url"`
}

type ChunkingConfig struct {
	ChunkSize   int               `json:"chunk_size"`
	Overlap     int               `json:"overlap"`
	Separators  []string          `json:"separators"`
	ParentChild ParentChildConfig `json:"parent_child"`
}

type ParentChildConfig struct {
	Enabled    bool `json:"enabled"`
	ParentSize int  `json:"parent_size"`
	ChildSize  int  `json:"child_size"`
}

type RetrievalConfig struct {
	Mode           string       `json:"mode"`
	TopK           int          `json:"top_k"`
	CandidateTopK  int          `json:"candidate_top_k"`
	ScoreThreshold float32      `json:"score_threshold"`
	DenseWeight    float32      `json:"dense_weight"`
	SparseWeight   float32      `json:"sparse_weight"`
	Rerank         RerankConfig `json:"rerank"`
}

type RerankConfig struct {
	Enabled bool    `json:"enabled"`
	ModelID *string `json:"model_id,omitempty"`
	TopN    int     `json:"top_n"`
}

func (cfg PipelineConfig) ValidateModelReferences() error {
	var vlmID *string
	if cfg.Parsing.VLM != nil {
		vlmID = cfg.Parsing.VLM.ModelID
	}
	for _, id := range []*string{vlmID, cfg.Retrieval.Rerank.ModelID} {
		if id != nil {
			if err := identity.Validate(*id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (cfg PipelineConfig) MarshalJSON() ([]byte, error) {
	if err := cfg.ValidateModelReferences(); err != nil {
		return nil, err
	}
	type payload PipelineConfig
	return json.Marshal(payload(cfg))
}

func (cfg *PipelineConfig) UnmarshalJSON(raw []byte) error {
	type payload PipelineConfig
	var decoded payload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	next := PipelineConfig(decoded)
	if err := next.ValidateModelReferences(); err != nil {
		return err
	}
	*cfg = next
	return nil
}

type Stage struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Retries   int    `json:"retries"`
	Error     string `json:"error"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
}

type RetrieveItem struct {
	ChunkID        string         `json:"chunk_id"`
	Content        string         `json:"content"`
	Score          float32        `json:"score"`
	DocID          string         `json:"doc_id"`
	DocTitle       string         `json:"doc_title"`
	KBID           string         `json:"kb_id"`
	KBName         string         `json:"kb_name"`
	MatchedContent string         `json:"matched_content"`
	Metadata       map[string]any `json:"metadata"`
}
