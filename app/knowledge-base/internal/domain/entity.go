package domain

import "time"

type KnowledgeBase struct {
	ID                int64          `json:"id" gorm:"primaryKey"`
	OwnerID           int64          `json:"owner_id"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	EmbeddingModel    string         `json:"embedding_model"`
	EmbeddingModelID  int64          `json:"embedding_model_id"`
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
	ID               int64           `json:"id" gorm:"primaryKey"`
	KBID             int64           `json:"kb_id"`
	Title            string          `json:"title"`
	FileType         string          `json:"file_type"`
	FileSize         int64           `json:"file_size"`
	OriginalFilename string          `json:"original_filename"`
	MinioBucket      string          `json:"minio_bucket"`
	MinioKey         string          `json:"minio_key"`
	ContentHash      string          `json:"content_hash"`
	Status           DocStatus       `json:"status"`
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
	ID            int64          `json:"id" gorm:"primaryKey"`
	DocID         int64          `json:"doc_id"`
	KBID          int64          `json:"kb_id"`
	ParentChunkID *int64         `json:"parent_chunk_id" gorm:"index"`
	ChunkIndex    int            `json:"chunk_index"`
	Content       string         `json:"content"`
	TokenCount    int            `json:"token_count"`
	MilvusDocID   string         `json:"milvus_doc_id"`
	Metadata      map[string]any `json:"metadata" gorm:"type:jsonb;serializer:json"`
	CreatedAt     time.Time      `json:"created_at"`
}

func (ChunkRecord) TableName() string {
	return "document_chunks"
}

type KnowledgeBinding struct {
	ID         int64     `json:"id" gorm:"primaryKey"`
	KBID       int64     `json:"kb_id"`
	KBName     string    `json:"kb_name" gorm:"<-:false"`
	TargetType string    `json:"target_type"`
	TargetID   int64     `json:"target_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func (KnowledgeBinding) TableName() string {
	return "knowledge_bindings"
}

type PipelineConfig struct {
	Preset    string          `json:"preset"`
	Parsing   ParsingConfig   `json:"parsing"`
	Chunking  ChunkingConfig  `json:"chunking"`
	Retrieval RetrievalConfig `json:"retrieval"`
	Wiki      WikiConfig      `json:"wiki"`
}

type WikiConfig struct {
	Enabled    bool   `json:"enabled"`
	ModelID    int64  `json:"model_id"`   // model registry ID, 0 = use default
	ModelName  string `json:"model_name"` // display only
	AutoLint   bool   `json:"auto_lint"`
	StaleHours int    `json:"stale_threshold_hours"`

	// 自动维护配置
	MaintenanceEnabled bool   `json:"maintenance_enabled"` // 是否启用自动维护
	MaintenanceCron    string `json:"maintenance_cron"`    // cron 表达式，如 "0 3 * * *"（每天 3 点）
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
	ModelID  int64  `json:"model_id,omitempty"`
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
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
	Enabled bool  `json:"enabled"`
	ModelID int64 `json:"model_id"`
	TopN    int   `json:"top_n"`
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
	ChunkID        int64          `json:"chunk_id"`
	Content        string         `json:"content"`
	Score          float32        `json:"score"`
	DocID          int64          `json:"doc_id"`
	DocTitle       string         `json:"doc_title"`
	KBID           int64          `json:"kb_id"`
	KBName         string         `json:"kb_name"`
	MatchedContent string         `json:"matched_content"`
	Metadata       map[string]any `json:"metadata"`
}
