package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database  config.DatabaseConfig  `json:"database"`
	AppRedis  config.RedisConfig     `json:"appRedis"`
	JWT       config.JWTConfig       `json:"jwt"`
	RateLimit config.RateLimitConfig `json:"rateLimit"`
}
