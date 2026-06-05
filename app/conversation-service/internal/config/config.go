package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database    config.DatabaseConfig  `json:"database"`
	Kafka       config.KafkaConfig     `json:"kafka"`
	Snowflake   SnowflakeConfig        `json:"snowflake"`
	Conv        ConvConfig             `json:"conv"`
	BotPlatform zrpc.RpcClientConf     `json:"botPlatform,optional"`
	UserService zrpc.RpcClientConf     `json:"userService,optional"`
	RateLimit   config.RateLimitConfig `json:"rateLimit"`
}

type SnowflakeConfig struct {
	WorkerID int64 `json:"workerId" default:"1"`
}

type ConvConfig struct {
	MaxMemberCount     int `json:"maxMemberCount" default:"500"`
	MaxGroupNameLen    int `json:"maxGroupNameLen" default:"64"`
	MaxAliasLen        int `json:"maxAliasLen" default:"32"`
	MaxAnnouncementLen int `json:"maxAnnouncementLen" default:"1024"`
}
