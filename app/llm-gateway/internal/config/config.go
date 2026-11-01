package config

import (
	"time"

	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database             config.DatabaseConfig `json:"database"`
	ModelConf            ModelConf             `json:"modelConf"`
	Capacity             CapacityConfig        `json:"capacity"`
	EncKey               string                `json:"encKey,optional"`
	UserService          zrpc.RpcClientConf    `json:"userService"`
	IngestEmbeddingToken string                `json:"ingestEmbeddingToken"`
}

type ModelConf struct {
	RefreshInterval time.Duration `json:"refreshInterval,default=300s"`
}

// CapacityConfig protects each process; it does not invent supplier quotas.
type CapacityConfig struct {
	MaxInFlight   int           `json:"maxInFlight,default=64"`
	LeaseDuration time.Duration `json:"leaseDuration,default=30s"`
	PollInterval  time.Duration `json:"pollInterval,default=50ms"`
}
