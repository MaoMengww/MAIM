package config

import (
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database    config.DatabaseConfig `json:"database"`
	ModelConf   ModelConf             `json:"modelConf"`
	RateLimit   RateLimitConf         `json:"rateLimit"`
	EncKey      string                `json:"encKey,optional"`
	Snowflake   SnowflakeConfig       `json:"snowflake"`
	UserService zrpc.RpcClientConf    `json:"userService"`
}

type SnowflakeConfig struct {
	WorkerID int64 `json:"workerId,default=1"`
}

type ModelConf struct {
	RefreshInterval time.Duration `json:"refreshInterval,default=300s"`
}

type RateLimitConf struct {
	DefaultRPM         int `json:"defaultRPM,default=100"`
	DefaultConcurrency int `json:"defaultConcurrency,default=10"`
}
