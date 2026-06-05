package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database  config.DatabaseConfig  `json:"database"`
	Kafka     config.KafkaConfig     `json:"kafka"`
	MinIO     config.MinIOConfig     `json:"minio"`
	Snowflake SnowflakeConfig        `json:"snowflake"`
	Audit     AuditConfig            `json:"audit"`
	RateLimit config.RateLimitConfig `json:"rateLimit"`
}

type SnowflakeConfig struct {
	WorkerID int64 `json:"workerId" default:"1"`
}

type AuditConfig struct {
	ExportMaxEvents int    `json:"exportMaxEvents" default:"10000"`
	ExportDir       string `json:"exportDir" default:"audit/exports"`
}
