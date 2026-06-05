package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database  config.DatabaseConfig  `json:"database"`
	MinIO     config.MinIOConfig     `json:"minio"`
	RateLimit config.RateLimitConfig `json:"rateLimit"`
}
