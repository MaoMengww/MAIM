package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database      config.DatabaseConfig  `json:"database"`
	AppRedis      config.RedisConfig     `json:"appRedis"`
	Milvus        MilvusConfig           `json:"milvus"`
	Minio         config.MinIOConfig     `json:"minio"`
	Kafka         config.KafkaConfig     `json:"kafka"`
	LLMGateway    LLMGatewayConfig       `json:"llmGateway"`
	RealtimeEvent RealtimeEventConfig    `json:"realtimeEvent"`
	MinerU        MinerUConfig           `json:"mineru"`
	Snowflake     SnowflakeConfig        `json:"snowflake"`
	Neo4j         Neo4jConfig            `json:"neo4j"`
	MaxFileSize   int64                  `json:"maxFileSize" default:"10485760"`
	RetryLimit    int                    `json:"retryLimit" default:"3"`
	RateLimit     config.RateLimitConfig `json:"rateLimit"`
}

type SnowflakeConfig struct {
	WorkerID int64 `json:"workerId" default:"1"`
}

type MilvusConfig struct {
	Address string `json:"address"`
	DBName  string `json:"dbName" default:"default"`
}

type LLMGatewayConfig = zrpc.RpcClientConf
type RealtimeEventConfig = zrpc.RpcClientConf

type MinerUConfig struct {
	URL     string `json:"url" default:"http://localhost:30000"`
	Timeout int    `json:"timeout" default:"60"`
}

type Neo4jConfig struct {
	URI      string `json:"uri"`
	Username string `json:"username" default:"neo4j"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}
