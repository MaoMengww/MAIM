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
	Driver      string `json:",default=postgres" yaml:"Driver"`
	DSN         string `json:"DSN"              yaml:"DSN"`
	MaxOpenConn int    `json:",default=100"     yaml:"MaxOpenConn"`
	MaxIdleConn int    `json:",default=10"      yaml:"MaxIdleConn"`
	MaxLifetime int    `json:",default=3600"    yaml:"MaxLifetime"`
	ReadDSN     string `json:",optional"        yaml:"ReadDSN"`
}

type RedisConfig struct {
	Host         string `json:",default=localhost:6379" yaml:"Host"`
	Password     string `json:",optional"               yaml:"Password"`
	DB           int    `json:",optional"               yaml:"DB"`
	PoolSize     int    `json:",default=100"            yaml:"PoolSize"`
	MinIdleConn  int    `json:",default=10"             yaml:"MinIdleConn"`
	DialTimeout  int    `json:",default=5"              yaml:"DialTimeout"`
	ReadTimeout  int    `json:",default=3"              yaml:"ReadTimeout"`
	WriteTimeout int    `json:",default=3"              yaml:"WriteTimeout"`
	ClusterMode  bool   `json:",optional"               yaml:"ClusterMode"`
	ClusterAddrs string `json:",optional"               yaml:"ClusterAddrs"`
}

type KafkaConfig struct {
	Brokers       []string `json:"Brokers"              yaml:"Brokers"`
	ConsumerGroup string   `json:",optional"        yaml:"ConsumerGroup"`
	Version       string   `json:",default=2.8.0"   yaml:"Version"`
	MaxRetry      int      `json:",default=3"       yaml:"MaxRetry"`
	SASLEnable    bool     `json:",optional"        yaml:"SASLEnable"`
	SASLUser      string   `json:",optional"        yaml:"SASLUser"`
	SASLPassword  string   `json:",optional"        yaml:"SASLPassword"`
}

type EtcdConfig struct {
	Hosts       []string `json:",optional"        yaml:"Hosts"`
	Key         string   `json:",optional"        yaml:"Key"`
	User        string   `json:",optional"        yaml:"User"`
	Password    string   `json:",optional"        yaml:"Password"`
	DialTimeout int      `json:",default=5"       yaml:"DialTimeout"`
	TTL         int      `json:",default=10"      yaml:"TTL"`
}

type MinIOConfig struct {
	Endpoint       string `json:"Endpoint"            yaml:"Endpoint"`
	PublicEndpoint string `json:",optional"        yaml:"PublicEndpoint"`
	AccessKey      string `json:"AccessKey"           yaml:"AccessKey"`
	SecretKey      string `json:"SecretKey"           yaml:"SecretKey"`
	UseSSL         bool   `json:",optional"        yaml:"UseSSL"`
	Bucket         string `json:",default=aim"     yaml:"Bucket"`
	Region         string `json:",optional"        yaml:"Region"`
}

type JWTConfig struct {
	Secret     string `json:"Secret"              yaml:"Secret"`
	ExpireSec  int    `json:",default=7200"    yaml:"ExpireSec"`
	RefreshSec int    `json:",default=604800"  yaml:"RefreshSec"`
}

type LogConfig struct {
	Level     string `json:",default=info"    yaml:"Level"`
	Format    string `json:",default=json"    yaml:"Format"`
	Output    string `json:",default=stdout"  yaml:"Output"`
	FilePath  string `json:",optional"        yaml:"FilePath"`
	MaxSize   int    `json:",default=100"     yaml:"MaxSize"`
	MaxBackup int    `json:",default=10"      yaml:"MaxBackup"`
	MaxAge    int    `json:",default=30"      yaml:"MaxAge"`
	Compress  bool   `json:",optional"        yaml:"Compress"`
	Path      string `json:",optional"        yaml:"Path"`
}

type ElasticsearchConfig struct {
	Addresses []string `json:"Addresses"           yaml:"Addresses"`
	Username  string   `json:",optional"        yaml:"Username"`
	Password  string   `json:",optional"        yaml:"Password"`
	CloudID   string   `json:",optional"        yaml:"CloudID"`
	APIKey    string   `json:",optional"        yaml:"APIKey"`
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
