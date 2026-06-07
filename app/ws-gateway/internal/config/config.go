package config

import (
	"os"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
	"gopkg.in/yaml.v3"
)

type (
	Config struct {
		Name           string             `yaml:"Name"`
		Host           string             `yaml:"Host"`
		Port           int                `yaml:"Port"`
		ListenOn       string             `yaml:"ListenOn"`
		Etcd           EtcdConfig         `yaml:"Etcd"`
		WebSocket      WebSocketConfig    `yaml:"WebSocket"`
		JWT            config.JWTConfig   `yaml:"JWT"`
		Redis          config.RedisConfig `yaml:"Redis"`
		BotPlatform    zrpc.RpcClientConf `yaml:"BotPlatform"`
		MessageService zrpc.RpcClientConf `yaml:"MessageService"`
		Metrics        MetricsConfig      `yaml:"Metrics"`
		Log            config.LogConfig   `yaml:"Log"`
		Telemetry      TelemetryConfig    `yaml:"Telemetry"`
	}

	EtcdConfig struct {
		Hosts []string `yaml:"Hosts"`
		Key   string   `yaml:"Key"`
	}

	WebSocketConfig struct {
		Host              string `yaml:"Host"`
		Port              int    `yaml:"Port"`
		ReadBufferSize    int    `yaml:"ReadBufferSize"`
		WriteBufferSize   int    `yaml:"WriteBufferSize"`
		HeartbeatInterval int    `yaml:"HeartbeatInterval"`
		MaxConn           int    `yaml:"MaxConn"`
	}

	MetricsConfig struct {
		Port int `yaml:"Port"`
	}

	TelemetryConfig struct {
		Name     string  `yaml:"Name"`
		Endpoint string  `yaml:"Endpoint"`
		Sampler  float64 `yaml:"Sampler"`
		Disabled bool    `yaml:"Disabled"`
	}
)

func defaultConfig() Config {
	return Config{
		Host:     "0.0.0.0",
		Port:     50060,
		ListenOn: "0.0.0.0:50060",
		WebSocket: WebSocketConfig{
			Host: "0.0.0.0", Port: 8081, ReadBufferSize: 4096, WriteBufferSize: 4096,
			HeartbeatInterval: 30, MaxConn: 10000,
		},
		Log: config.LogConfig{Level: "info", Format: "json", Output: "stdout"},
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
