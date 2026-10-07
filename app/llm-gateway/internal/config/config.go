package config

import (
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database       config.DatabaseConfig `json:"database"`
	ModelConf      ModelConf             `json:"modelConf"`
	RateLimit      RateLimitConf         `json:"rateLimit"`
	EncKey         string                `json:"encKey,optional"`
	UserService    zrpc.RpcClientConf    `json:"userService"`
	EmbeddingQuota EmbeddingQuotaConfig  `json:"embeddingQuota"`
}

type ModelConf struct {
	RefreshInterval time.Duration `json:"refreshInterval,default=300s"`
}

type RateLimitConf struct {
	DefaultRPM         int `json:"defaultRPM,default=100"`
	DefaultConcurrency int `json:"defaultConcurrency,default=10"`
}

type EmbeddingQuotaConfig struct {
	OnlineRPM         int    `json:"onlineRPM,default=80"`
	OnlineConcurrency int    `json:"onlineConcurrency,default=8"`
	IngestRPM         int    `json:"ingestRPM,default=20"`
	IngestConcurrency int    `json:"ingestConcurrency,default=2"`
	IngestToken       string `json:"ingestToken,optional"`
}
