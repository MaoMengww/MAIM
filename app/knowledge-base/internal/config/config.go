package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database    config.DatabaseConfig  `json:"database"`
	AppRedis    config.RedisConfig     `json:"appRedis"`
	Milvus      MilvusConfig           `json:"milvus"`
	Minio       config.MinIOConfig     `json:"minio"`
	Kafka       config.KafkaConfig     `json:"kafka"`
	LLMGateway  LLMGatewayConfig       `json:"llmGateway"`
	MinerU      MinerUConfig           `json:"mineru"`
	MaxFileSize int64                  `json:"maxFileSize" default:"10485760"`
	RetryLimit  int                    `json:"retryLimit" default:"3"`
	RateLimit   config.RateLimitConfig `json:"rateLimit"`
	Ingest      IngestConfig           `json:"ingest"`
}

type IngestConfig struct {
	RequestsPerSecond int    `json:"requestsPerSecond,default=5"`
	Concurrency       int    `json:"concurrency,default=2"`
	MetricsPort       int    `json:"metricsPort,default=9118"`
	EmbeddingToken    string `json:"embeddingToken,optional"`
}

type MilvusConfig struct {
	Address string `json:"address"`
	DBName  string `json:"dbName" default:"default"`
}

type LLMGatewayConfig = zrpc.RpcClientConf

type MinerUConfig struct {
	URL     string `json:"url" default:"http://localhost:30000"`
	Timeout int    `json:"timeout" default:"60"`
}
