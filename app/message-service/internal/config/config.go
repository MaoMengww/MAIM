package config

import (
	"github.com/maomeng/aim/pkg/config"
	"github.com/zeromicro/go-zero/zrpc"
)

type Config struct {
	zrpc.RpcServerConf
	Database      config.DatabaseConfig      `json:"database"`
	Kafka         config.KafkaConfig         `json:"kafka"`
	Elasticsearch config.ElasticsearchConfig `json:"elasticsearch"`
	Message       MessageConfig              `json:"message"`
	Conv          ConvConfig                 `json:"conv"`
	RateLimit     config.RateLimitConfig     `json:"rateLimit"`
}

type ConvConfig struct {
	MaxMemberCount     int `json:"maxMemberCount" default:"500"`
	MaxGroupNameLen    int `json:"maxGroupNameLen" default:"64"`
	MaxAliasLen        int `json:"maxAliasLen" default:"32"`
	MaxAnnouncementLen int `json:"maxAnnouncementLen" default:"1024"`
}

type MessageConfig struct {
	RecallWindowSeconds int `json:"recallWindowSeconds" default:"120"`
	EditWindowSeconds   int `json:"editWindowSeconds" default:"120"`
	MaxPageSize         int `json:"maxPageSize" default:"100"`
	InboxRetentionDays  int `json:"inboxRetentionDays,default=30"`
}
