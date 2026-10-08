package config

import (
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Role           string                 `json:"-"`
	JWT            config.JWTConfig       `json:"jwt,optional"`
	EncryptionKey  string                 `json:"encryptionKey,optional"`
	Database       config.DatabaseConfig  `json:"database"`
	Kafka          config.KafkaConfig     `json:"kafka"`
	Bot            BotConfig              `json:"bot"`
	LlmGateway     zrpc.RpcClientConf     `json:"llmGateway"`
	MessageService zrpc.RpcClientConf     `json:"messageService"`
	KnowledgeBase  zrpc.RpcClientConf     `json:"knowledgeBase"`
	FileService    zrpc.RpcClientConf     `json:"fileService,optional"`
	Runtime        zrpc.RpcClientConf     `json:"runtime,optional"`
	UserService    zrpc.RpcClientConf     `json:"userService"`
	RateLimit      config.RateLimitConfig `json:"rateLimit"`
}

type BotConfig struct {
	MaxContextMessages int           `json:"maxContextMessages,default=10"`
	MaxToolRounds      int           `json:"maxToolRounds,default=3"`
	WorkerPoolSize     int           `json:"workerPoolSize,default=0"`
	RequestTimeout     time.Duration `json:"requestTimeout,default=120s"`
}
