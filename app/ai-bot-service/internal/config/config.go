package config

import (
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database       config.DatabaseConfig  `json:"database"`
	Kafka          config.KafkaConfig     `json:"kafka"`
	Bot            BotConfig              `json:"bot"`
	LlmGateway     zrpc.RpcClientConf     `json:"llmGateway"`
	MessageService zrpc.RpcClientConf     `json:"messageService"`
	KnowledgeBase  zrpc.RpcClientConf     `json:"knowledgeBase"`
	WsGateway      zrpc.RpcClientConf     `json:"wsGateway"`
	BotPlatform    zrpc.RpcClientConf     `json:"botPlatform"`
	UserService    zrpc.RpcClientConf     `json:"userService"`
	Milvus         MilvusConfig           `json:"milvus"`
	Memory         MemoryConfig           `json:"memory"`
	RateLimit      config.RateLimitConfig `json:"rateLimit"`
}

type BotConfig struct {
	MaxContextMessages int           `json:"maxContextMessages,default=10"`
	MaxToolRounds      int           `json:"maxToolRounds,default=3"`
	WorkerPoolSize     int           `json:"workerPoolSize,default=0"`
	RequestTimeout     time.Duration `json:"requestTimeout,default=120s"`
}

type MilvusConfig struct {
	Host     string `json:"host,default=localhost"`
	Port     int    `json:"port,default=19530"`
	Database string `json:"database,default=aim"`
}

type MemoryConfig struct {
	Neo4j            Neo4jConfig `json:"neo4j"`
	WorkerID         int64       `json:"workerId,default=8"`
	EmbeddingDim     int         `json:"embeddingDim,default=1536"`
	VectorCollection string      `json:"vectorCollection,default=bot_memory_facts_v1"`
	VectorTopKMult   int         `json:"vectorTopKMult,default=3"`
}

type Neo4jConfig struct {
	URI      string `json:"uri,default=bolt://localhost:7687"`
	Username string `json:"username,default=neo4j"`
	Password string `json:"password"`
	Database string `json:"database,default=neo4j"`
}
