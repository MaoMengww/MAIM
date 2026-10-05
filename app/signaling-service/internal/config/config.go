package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Kafka          config.KafkaConfig     `json:"kafka"`
	Database       config.DatabaseConfig  `json:"database"`
	MessageService zrpc.RpcClientConf     `json:"messageService"`
	BotService     zrpc.RpcClientConf     `json:"botService"`
	WsGateway      zrpc.RpcClientConf     `json:"wsGateway"`
	Push           PushConfig             `json:"push"`
	RateLimit      config.RateLimitConfig `json:"rateLimit"`
}

type PushConfig struct {
	FCMEnabled     bool   `json:"fcmEnabled"`
	FCMCredentials string `json:"fcmCredentials"`
	APNSEnabled    bool   `json:"apnsEnabled"`
	APNSKeyFile    string `json:"apnsKeyFile"`
	APNSKeyID      string `json:"apnsKeyID"`
	APNSTeamID     string `json:"apnsTeamID"`
	APNSTopic      string `json:"apnsTopic"`
}
