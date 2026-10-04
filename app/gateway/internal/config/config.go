package config

import (
	"github.com/maomeng/aim/pkg/config"
)

// ServicesConfig holds the gRPC targets of every backend the gateway dials.
// Targets are full gRPC targets (e.g. dns:///user-service:50051) supplied by
// the environment, so no service registry is involved.
type ServicesConfig struct {
	UserServiceAddr         string `json:"userServiceAddr"`
	MessageServiceAddr      string `json:"messageServiceAddr"`
	FileServiceAddr         string `json:"fileServiceAddr"`
	BotPlatformServiceAddr  string `json:"botPlatformServiceAddr"`
	KnowledgeServiceAddr    string `json:"knowledgeServiceAddr"`
	NotificationServiceAddr string `json:"notificationServiceAddr"`
	AIBotServiceAddr        string `json:"aiBotServiceAddr"`
	LLMGatewayServiceAddr   string `json:"llmGatewayServiceAddr"`
}

type RateLimitConfig struct {
	Enabled           bool `json:"enabled,default=true"`
	RequestsPerSecond int  `json:"requestsPerSecond,default=100"`
	MessagePerSecond  int  `json:"messagePerSecond,default=10"`
}

type TimeoutConfig struct {
	DefaultMs int `json:"defaultMs,default=3000"`
	BotMs     int `json:"botMs,default=60000"`
}

type MetricsConfig struct {
	Port int `json:"port,default=9091"`
}

type TelemetryConfig struct {
	Name     string  `json:"name,optional"`
	Endpoint string  `json:"endpoint,optional"`
	Sampler  float64 `json:"sampler,optional"`
	Disabled bool    `json:"disabled,optional"`
}

type Config struct {
	Name      string             `json:"name,optional"`
	Host      string             `json:"host,default=0.0.0.0"`
	Port      int                `json:"port,default=8080"`
	JWT       config.JWTConfig   `json:"jwt"`
	Services  ServicesConfig     `json:"services"`
	RateLimit RateLimitConfig    `json:"rateLimit"`
	Timeout   TimeoutConfig      `json:"timeout"`
	Redis     config.RedisConfig `json:"redis"`
	Metrics   MetricsConfig      `json:"metrics"`
	Log       config.LogConfig   `json:"log"`
	Telemetry TelemetryConfig    `json:"telemetry"`
}
