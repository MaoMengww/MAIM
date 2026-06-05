package config

import (
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database       config.DatabaseConfig  `json:"database"`
	Kafka          config.KafkaConfig     `json:"kafka"`
	Bot            BotConfig              `json:"bot"`
	LlmGateway     zrpc.RpcClientConf     `json:"llmGateway"`
	MessageService zrpc.RpcClientConf     `json:"messageService"`
	KnowledgeBase  zrpc.RpcClientConf     `json:"knowledgeBase"`
	WsGateway      zrpc.RpcClientConf     `json:"wsGateway"`
	BotPlatform    zrpc.RpcClientConf     `json:"botPlatform"`
	UserService    zrpc.RpcClientConf     `json:"userService"`
	Milvus         MilvusConfig           `json:"milvus"`
	RateLimit      config.RateLimitConfig `json:"rateLimit"`
}

type BotConfig struct {
	MaxContextMessages int           `json:"maxContextMessages,default=10"`
	MaxToolRounds      int           `json:"maxToolRounds,default=3"`
	WorkerPoolSize     int           `json:"workerPoolSize,default=0"`
	RequestTimeout     time.Duration `json:"requestTimeout,default=120s"`
}

type MilvusConfig struct {
	Host     string `json:"host,default=localhost"`
	Port     int    `json:"port,default=19530"`
	Database string `json:"database,default=aim"`
}
