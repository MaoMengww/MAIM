package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database       config.DatabaseConfig  `json:"database"`
	Snowflake      SnowflakeConfig        `json:"snowflake"`
	JWT            config.JWTConfig       `json:"jwt,optional"`
	EncryptionKey  string                 `json:"encryptionKey,optional"`
	MessageService zrpc.RpcClientConf     `json:"messageService,optional"`
	RateLimit      config.RateLimitConfig `json:"rateLimit"`
}

type SnowflakeConfig struct {
	WorkerID int64 `json:"workerId,default=6"`
}
