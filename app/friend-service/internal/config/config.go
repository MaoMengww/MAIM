package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database  config.DatabaseConfig  `json:"database"`
	UserRPC   zrpc.RpcClientConf     `json:"userRPC"`
	RateLimit config.RateLimitConfig `json:"rateLimit"`
}
