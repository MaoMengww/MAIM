package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	Name           string                `json:"Name"`
	Host           string                `json:"Host,default=0.0.0.0"`
	Port           int                   `json:"Port,default=8081"`
	ListenOn       string                `json:"ListenOn,default=0.0.0.0:50059"`
	WebSocket      WebSocketConfig       `json:"WebSocket"`
	JWT            config.JWTConfig      `json:"JWT"`
	Redis          config.RedisConfig    `json:"Redis"`
	Database       config.DatabaseConfig `json:"Database"`
	Kafka          config.KafkaConfig    `json:"Kafka"`
	BotService     zrpc.RpcClientConf    `json:"BotService"`
	MessageService zrpc.RpcClientConf    `json:"MessageService"`
	Metrics        MetricsConfig         `json:"Metrics"`
	Log            config.LogConfig      `json:"Log"`
	Telemetry      TelemetryConfig       `json:"Telemetry"`
	StreamCache    StreamCacheConfig     `json:"StreamCache"`
	Registry       RegistryConfig        `json:"Registry"`
	Shutdown       ShutdownConfig        `json:"Shutdown"`
	Push           PushConfig            `json:"Push"`
}
type WebSocketConfig struct {
	ReadBufferSize      int `json:"ReadBufferSize,default=1024"`
	WriteBufferSize     int `json:"WriteBufferSize,default=1024"`
	HeartbeatInterval   int `json:"HeartbeatInterval,default=30"`
	WriteTimeoutSeconds int `json:"WriteTimeoutSeconds,default=5"`
	MaxConn             int `json:"MaxConn,default=10000"`
}
type RegistryConfig struct {
	InstanceID string `json:"InstanceID,optional"`
	TTLSeconds int    `json:"TTLSeconds,default=120"`
}
type ShutdownConfig struct {
	ReadinessDelaySeconds int `json:"ReadinessDelaySeconds,default=5"`
	DrainSeconds          int `json:"DrainSeconds,default=20"`
	TimeoutSeconds        int `json:"TimeoutSeconds,default=30"`
}
type MetricsConfig struct {
	Port int `json:"Port,default=9103"`
}
type TelemetryConfig struct {
	Name     string  `json:"Name,optional"`
	Endpoint string  `json:"Endpoint,optional"`
	Sampler  float64 `json:"Sampler,optional"`
	Disabled bool    `json:"Disabled,optional"`
}
type StreamCacheConfig struct {
	Enabled            bool `json:"Enabled,default=true"`
	TTLSeconds         int  `json:"TTLSeconds,default=300"`
	MaxChunksPerStream int  `json:"MaxChunksPerStream,default=1000"`
}
type PushConfig struct {
	FCMEnabled     bool   `json:"FCMEnabled,optional"`
	FCMCredentials string `json:"FCMCredentials,optional"`
	APNSEnabled    bool   `json:"APNSEnabled,optional"`
	APNSKeyFile    string `json:"APNSKeyFile,optional"`
	APNSKeyID      string `json:"APNSKeyID,optional"`
	APNSTeamID     string `json:"APNSTeamID,optional"`
	APNSTopic      string `json:"APNSTopic,optional"`
}
