package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type (
	Config struct {
		Name           string             `json:"Name"`
		Host           string             `json:"Host"`
		Port           int                `json:"Port"`
		ListenOn       string             `json:"ListenOn"`
		WebSocket      WebSocketConfig    `json:"WebSocket"`
		JWT            config.JWTConfig   `json:"JWT"`
		Redis          config.RedisConfig `json:"Redis"`
		BotService     zrpc.RpcClientConf `json:"BotService"`
		MessageService zrpc.RpcClientConf `json:"MessageService"`
		Metrics        MetricsConfig      `json:"Metrics"`
		Log            config.LogConfig   `json:"Log"`
		Telemetry      TelemetryConfig    `json:"Telemetry"`
		StreamCache    StreamCacheConfig  `json:"StreamCache"`
	}

	WebSocketConfig struct {
		Host              string `json:"Host"`
		Port              int    `json:"Port"`
		ReadBufferSize    int    `json:"ReadBufferSize"`
		WriteBufferSize   int    `json:"WriteBufferSize"`
		HeartbeatInterval int    `json:"HeartbeatInterval"`
		MaxConn           int    `json:"MaxConn"`
	}

	MetricsConfig struct {
		Port int `json:"Port"`
	}

	TelemetryConfig struct {
		Name     string  `json:"Name"`
		Endpoint string  `json:"Endpoint"`
		Sampler  float64 `json:"Sampler"`
		Disabled bool    `json:"Disabled,optional"`
	}

	StreamCacheConfig struct {
		Enabled            bool `json:"Enabled"`
		TTLSeconds         int  `json:"TTLSeconds"`
		MaxChunksPerStream int  `json:"MaxChunksPerStream"`
	}
)
