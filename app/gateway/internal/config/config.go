package config

import (
	"os"

	"github.com/maomeng/aim/pkg/config"
	"gopkg.in/yaml.v3"
)

type ServicesConfig struct {
	UserServiceKey         string `json:"userServiceKey"         yaml:"UserServiceKey"         default:"user.rpc"`
	FriendServiceKey       string `json:"friendServiceKey"       yaml:"FriendServiceKey"       default:"friend.rpc"`
	ConversationServiceKey string `json:"conversationServiceKey" yaml:"ConversationServiceKey" default:"conversation.rpc"`
	MessageServiceKey      string `json:"messageServiceKey"      yaml:"MessageServiceKey"      default:"message.rpc"`
	FileServiceKey         string `json:"fileServiceKey"         yaml:"FileServiceKey"         default:"file.rpc"`
	BotPlatformServiceKey  string `json:"botPlatformServiceKey"  yaml:"BotPlatformServiceKey"  default:"bot-platform.rpc"`
	KnowledgeServiceKey    string `json:"knowledgeServiceKey"    yaml:"KnowledgeServiceKey"    default:"knowledge.rpc"`
	NotificationServiceKey string `json:"notificationServiceKey" yaml:"NotificationServiceKey" default:"ws-gateway"`
	AuditServiceKey        string `json:"auditServiceKey"        yaml:"AuditServiceKey"        default:"audit.rpc"`
	AIBotServiceKey        string `json:"aiBotServiceKey"        yaml:"AIBotServiceKey"        default:"aibot.rpc"`
	LLMGatewayServiceKey   string `json:"llmGatewayServiceKey"   yaml:"LLMGatewayServiceKey"   default:"llm-gateway.rpc"`
}

type RateLimitConfig struct {
	Enabled           bool `json:"enabled"            yaml:"Enabled"            default:"true"`
	RequestsPerSecond int  `json:"requestsPerSecond"  yaml:"RequestsPerSecond"  default:"100"`
	MessagePerSecond  int  `json:"messagePerSecond"   yaml:"MessagePerSecond"   default:"10"`
}

type TimeoutConfig struct {
	DefaultMs int `json:"defaultMs" yaml:"DefaultMs" default:"3000"`
	BotMs     int `json:"botMs"     yaml:"BotMs"     default:"60000"`
}

type MetricsConfig struct {
	Port int `json:"port" yaml:"Port" default:"9091"`
}

type TelemetryConfig struct {
	Name     string  `json:"name,omitempty"     yaml:"Name"`
	Endpoint string  `json:"endpoint,omitempty" yaml:"Endpoint"`
	Sampler  float64 `json:"sampler,omitempty"  yaml:"Sampler"`
	Disabled bool    `json:"disabled,omitempty" yaml:"Disabled"`
}

type Config struct {
	Name      string                `json:"name,omitempty"      yaml:"Name"`
	Host      string                `json:"host,omitempty"      yaml:"Host"    default:"0.0.0.0"`
	Port      int                   `json:"port,omitempty"      yaml:"Port"    default:"8080"`
	JWT       config.JWTConfig      `json:"jwt"                 yaml:"JWT"`
	Etcd      config.EtcdConfig     `json:"etcd"                yaml:"Etcd"`
	Database  config.DatabaseConfig `json:"database"            yaml:"Database"`
	Services  ServicesConfig        `json:"services"            yaml:"Services"`
	RateLimit RateLimitConfig       `json:"rateLimit"           yaml:"RateLimit"`
	Timeout   TimeoutConfig         `json:"timeout"             yaml:"Timeout"`
	Redis     config.RedisConfig    `json:"redis"               yaml:"Redis"`
	Metrics   MetricsConfig         `json:"metrics"             yaml:"Metrics"`
	Log       config.LogConfig      `json:"log"                 yaml:"Log"`
	Telemetry TelemetryConfig       `json:"telemetry"           yaml:"Telemetry"`
	EncKey    string                `json:"encKey"              yaml:"EncKey"`
}

func defaultConfig() Config {
	return Config{
		Host: "0.0.0.0",
		Port: 8080,
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 100,
			MessagePerSecond:  10,
		},
		Timeout: TimeoutConfig{
			DefaultMs: 3000,
			BotMs:     60000,
		},
		Metrics: MetricsConfig{
			Port: 9091,
		},
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
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
