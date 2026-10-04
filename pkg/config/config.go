package config

// Shared configuration value types used by the service Config structs.
// Loading is done by the framework loader (github.com/zeromicro/go-zero/core/conf),
// which reads YAML/JSON/TOML and expands ${ENV} placeholders via conf.UseEnv.

type DatabaseConfig struct {
	Driver      string `json:",default=postgres"`
	DSN         string `json:"DSN"`
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
	Brokers       []string `json:"Brokers"`
	ConsumerGroup string   `json:",optional"`
	Version       string   `json:",default=2.8.0"`
	MaxRetry      int      `json:",default=3"`
	SASLEnable    bool     `json:",optional"`
	SASLUser      string   `json:",optional"`
	SASLPassword  string   `json:",optional"`
}

type MinIOConfig struct {
	Endpoint       string `json:"Endpoint"`
	PublicEndpoint string `json:",optional"`
	AccessKey      string `json:"AccessKey"`
	SecretKey      string `json:"SecretKey"`
	UseSSL         bool   `json:",optional"`
	Bucket         string `json:",default=aim"`
	Region         string `json:",optional"`
}

type JWTConfig struct {
	Secret     string `json:"Secret"`
	ExpireSec  int    `json:",default=7200"`
	RefreshSec int    `json:",default=604800"`
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
	Addresses []string `json:"Addresses"`
	Username  string   `json:",optional"`
	Password  string   `json:",optional"`
	CloudID   string   `json:",optional"`
	APIKey    string   `json:",optional"`
}

type RateLimitConfig struct {
	Enabled           bool `json:",default=true"`
	RequestsPerSecond int  `json:",default=100"`
	Concurrency       int  `json:",default=10"`
}
