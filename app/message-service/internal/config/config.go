package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database      config.DatabaseConfig      `json:"database"`
	Kafka         config.KafkaConfig         `json:"kafka"`
	Elasticsearch config.ElasticsearchConfig `json:"elasticsearch"`
	Message       MessageConfig              `json:"message"`
	Snowflake     SnowflakeConfig            `json:"snowflake"`
	ConvService   zrpc.RpcClientConf         `json:"convService"`
	UserService   zrpc.RpcClientConf         `json:"userService"`
	BotPlatform   zrpc.RpcClientConf         `json:"botPlatform"`
	FriendService zrpc.RpcClientConf         `json:"friendService"`
	RateLimit     config.RateLimitConfig     `json:"rateLimit"`
}

type MessageConfig struct {
	RecallWindowSeconds int `json:"recallWindowSeconds" default:"120"`
	EditWindowSeconds   int `json:"editWindowSeconds" default:"120"`
	MaxPageSize         int `json:"maxPageSize" default:"100"`
}

type SnowflakeConfig struct {
	WorkerID int64 `json:"workerId" default:"1"`
}

type ConvServiceConfig = zrpc.RpcClientConf
