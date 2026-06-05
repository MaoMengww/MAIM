package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeromicro/go-zero/core/conf"
)

type ServerConfig struct {
	Name string `json:",optional"`
	Host string `json:",default=0.0.0.0"`
	Port int    `json:",default=8080"`
	Mode string `json:",default=release"`
}

type DatabaseConfig struct {
	Driver      string `json:",default=postgres"`
	DSN         string
	MaxOpenConn int    `json:",default=100"`
	MaxIdleConn int    `json:",default=10"`
	MaxLifetime int    `json:",default=3600"`
	ReadDSN     string `json:",optional"`
}

type RedisConfig struct {
	Host         string `json:",default=localhost:6379"`
	Password     string `json:",optional"`
	DB           int    `json:",optional"`
	PoolSize     int    `json:",default=100"`
	MinIdleConn  int    `json:",default=10"`
	DialTimeout  int    `json:",default=5"`
	ReadTimeout  int    `json:",default=3"`
	WriteTimeout int    `json:",default=3"`
	ClusterMode  bool   `json:",optional"`
	ClusterAddrs string `json:",optional"`
}

type KafkaConfig struct {
	Brokers       []string
	ConsumerGroup string `json:",optional"`
	Version       string `json:",default=2.8.0"`
	MaxRetry      int    `json:",default=3"`
	SASLEnable    bool   `json:",optional"`
	SASLUser      string `json:",optional"`
	SASLPassword  string `json:",optional"`
}

type EtcdConfig struct {
	Hosts       []string
	Key         string `json:",optional"`
	User        string `json:",optional"`
	Password    string `json:",optional"`
	DialTimeout int    `json:",default=5"`
	TTL         int    `json:",default=10"`
}

type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool   `json:",optional"`
	Bucket    string `json:",default=aim"`
	Region    string `json:",optional"`
}

type JWTConfig struct {
	Secret     string
	ExpireSec  int `json:",default=7200"`
	RefreshSec int `json:",default=604800"`
}

type LogConfig struct {
	Level     string `json:",default=info"`
	Format    string `json:",default=json"`
	Output    string `json:",default=stdout"`
	FilePath  string `json:",optional"`
	MaxSize   int    `json:",default=100"`
	MaxBackup int    `json:",default=10"`
	MaxAge    int    `json:",default=30"`
	Compress  bool   `json:",optional"`
	Path      string `json:",optional"`
}

type ElasticsearchConfig struct {
	Addresses []string
	Username  string `json:",optional"`
	Password  string `json:",optional"`
	CloudID   string `json:",optional"`
	APIKey    string `json:",optional"`
}

type RateLimitConfig struct {
	Enabled           bool `json:",default=true"`
	RequestsPerSecond int  `json:",default=100"`
	Concurrency       int  `json:",default=10"`
}

type TraceConfig struct {
	Endpoint string  `json:",default=localhost:4317"`
	Sampler  string  `json:",default=always_on"`
	Ratio    float64 `json:",default=1.0"`
}

type Config struct {
	Server   ServerConfig   `json:"server,optional"`
	Database DatabaseConfig `json:"database,optional"`
	Redis    RedisConfig    `json:"redis,optional"`
	Log      LogConfig      `json:"log,optional"`
	Trace    TraceConfig    `json:"trace,optional"`
	JWT      JWTConfig      `json:"jwt,optional"`
	MinIO    MinIOConfig    `json:"minio,optional"`
}

// Option configures Load behavior.
type Option func(*options)

type options struct {
	useEnv bool
}

// WithEnv enables environment variable substitution in config values.
func WithEnv() Option {
	return func(o *options) {
		o.useEnv = true
	}
}

// Load reads a JSON config file and unmarshals it into v.
func Load(path string, v any, opts ...Option) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".json" {
		return fmt.Errorf("unrecognized config file type: %s", ext)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config read failed: %w", err)
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	data := string(content)
	if o.useEnv {
		data = os.ExpandEnv(data)
	}

	// Pre-fill defaults so optional sub-structs still get default values
	if err := conf.FillDefault(v); err != nil {
		return err
	}

	return conf.LoadFromJsonBytes([]byte(data), v)
}

// MustLoad reads a config file and panics on error.
func MustLoad(path string, v any, opts ...Option) {
	if err := Load(path, v, opts...); err != nil {
		panic(err)
	}
}
