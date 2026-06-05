package consts

import "time"

// ---- Service names ----

const (
	ServiceNameGateway      = "gateway"
	ServiceNameWsGateway    = "ws-gateway"
	ServiceNameUser         = "user-service"
	ServiceNameFriend       = "friend-service"
	ServiceNameConversation = "conversation-service"
	ServiceNameMessage      = "message-service"
	ServiceNameBot          = "bot-service"
	ServiceNameKnowledge    = "knowledge-service"
	ServiceNameFile         = "file-service"
	ServiceNameAudit        = "audit-service"
	ServiceNameSignaling    = "signaling-service"
)

// ---- Bot types ----

const (
	BotTypeOfficial     = "official"
	BotTypeSelfDeployed = "self_deployed"
	BotTypeThirdParty   = "third_party"
)

// ---- Service ports ----

const (
	PortGateway       = 8080
	PortWsGateway     = 8081
	PortUser          = 50051
	PortConversation  = 50052
	PortMessage       = 50053
	PortBot           = 50055
	PortKnowledge     = 50057
	PortFile          = 50058
	PortAudit         = 50059
	PortWsGatewayGRPC = 50060
	PortSignaling     = 50061
	PortMetrics       = 9090
)

// ---- Message types (aligned with protobuf) ----

const (
	MsgTypeUnspecified int32 = 0
	MsgTypeText        int32 = 1
	MsgTypeImage       int32 = 2
	MsgTypeFile        int32 = 3
	MsgTypeVideo       int32 = 4
	MsgTypeAudio       int32 = 5
	MsgTypeLocation    int32 = 6
	MsgTypeSystem      int32 = 7
	MsgTypeCustom      int32 = 8
	MsgTypeBot         int32 = 9
)

// ---- Message status ----

const (
	MsgStatusNormal    int32 = 1
	MsgStatusRecalled  int32 = 2
	MsgStatusEdited    int32 = 3
	MsgStatusStreaming int32 = 4
)

// ---- Friend request status ----

const (
	FriendReqUnspecified = 0
	FriendReqPending     = 1
	FriendReqAccepted    = 2
	FriendReqRejected    = 3
	FriendReqCancelled   = 4
)

// ---- Presence status ----

const (
	PresenceOnline  = "online"
	PresenceOffline = "offline"
)

// ---- WebSocket events ----

const (
	// Client to Server
	EventPing                = "ping"
	EventSubscribePresence   = "subscribe_presence"
	EventUnsubscribePresence = "unsubscribe_presence"
	EventSync                = "sync"
	EventTyping              = "typing"
	EventTypingStop          = "typing_stop"
	EventAck                 = "ack"

	// Server to Client
	EventPong             = "pong"
	EventMessageNew       = "message.new"
	EventMessageRecalled  = "message.recalled"
	EventMessageEdited    = "message.edited"
	EventPresence         = "presence"
	EventReadSync         = "read_sync"
	EventTypingNotify     = "typing"
	EventTypingStopNotify = "typing.stop"
	EventBotTyping        = "bot.typing"
	EventNotificationNew  = "notification.new"
	EventSyncResp         = "sync_resp"
	EventUnreadCount      = "unread_count"
	EventReadReceipt      = "read_receipt"
)

// ---- WebSocket timing & buffer constants ----

const (
	WSPingPeriod    = 30 * time.Second
	WSPongWait      = 60 * time.Second
	WSWriteWait     = 10 * time.Second
	WSReadWait      = 90 * time.Second
	TypingTTL       = 3 * time.Second
	WSPingPeriodSec = 30
	WSPongWaitSec   = 60
	WSWriteWaitSec  = 10
	WSReadWaitSec   = 90
)

const (
	WSReadBufferSize   = 4096
	WSWriteBufferSize  = 4096
	WSMaxMessageSize   = 4096
	WSWriteChBuffer    = 256
	WSRecvChBuffer     = 256
	WSRegisterChBuffer = 64
	WSMaxConn          = 10000
	WSSyncLimit        = 200
)

// ---- gRPC limits ----

const (
	GRPCMaxRecvMsgSize = 4 * 1024 * 1024 // 4 MB
	GRPCMaxSendMsgSize = 4 * 1024 * 1024 // 4 MB
)

// ---- Notification types ----

const (
	NotifTypeSystem = 1
	NotifTypeAudit  = 2
	NotifTypeBot    = 3
)

// ---- HTTP status codes ----

const (
	StatusCodeSuccess      = 0
	StatusCodeBadRequest   = 400
	StatusCodeUnauthorized = 401
	StatusCodeForbidden    = 403
	StatusCodeNotFound     = 404
	StatusCodeConflict     = 409
	StatusCodeInternal     = 500
	StatusCodeServiceDown  = 503
)

// ---- Pagination defaults ----

const (
	DefaultPageSize   = 20
	MaxPageSize       = 100
	DefaultTimeoutSec = 30
	MaxRetryTimes     = 3
	DefaultTokenTTL   = 7200
	MaxFileSize       = 100 * 1024 * 1024 // 100 MB
)

// ---- Retry & backoff ----

const (
	RetryMaxAttempts   = 3
	RetryBaseBackoffMs = 100
	RetryMaxBackoffMs  = 5000
)

// ---- DLQ ----

const (
	DLQSuffix     = ".dlq"
	DLQTimeoutSec = 5
)

// ---- JWT / Token ----

const (
	JWTIssuer            = "aim"
	JWTDefaultExpireSec  = 3600
	JWTDefaultRefreshSec = 2592000 // 30 days
	JWTRefreshTokenTTL   = 30 * 24 * time.Hour
	JWTReplacedTTL       = 1 * time.Hour
	MsgIdempotentTTL     = 7 * 24 * time.Hour
)

// ---- Token states ----

const (
	TokenStateActive     = "active"
	TokenStateRefreshing = "refreshing"
	TokenStateReplaced   = "replaced"
)

// ---- HTTP headers ----

const (
	HeaderRequestID    = "X-Request-ID"
	HeaderTraceID      = "X-Trace-ID"
	HeaderToken        = "Authorization"
	HeaderUserID       = "X-User-ID"
	HeaderContentType  = "Content-Type"
	HeaderAIMSignature = "X-AIM-Signature"
	HeaderAIMTimestamp = "X-AIM-Timestamp"
)

// ---- Webhook ----

const (
	WebhookCallbackTimeout = 30 // seconds, used as time.Duration multiplier
	WebhookMaxRetries      = 3
	WebhookReplayWindowSec = 3600
)

// ---- Content types ----

const (
	ContentTypeJSON     = "application/json"
	ContentTypeProtobuf = "application/protobuf"
)

// ---- gRPC metadata keys ----

const (
	MetadataKeyUserID    = "user-id"
	MetadataKeyDeviceID  = "device-id"
	MetadataKeyRequestID = "request-id"
)

// ---- WS query parameter names ----

const (
	QueryParamToken    = "token"
	QueryParamDeviceID = "device_id"
	QueryParamPlatform = "platform"
)

// ---- WS HTTP paths ----

const (
	WSPath       = "/ws"
	WSHealthPath = "/health"
	MetricsPath  = "/metrics"
)

// ---- Metrics ----

const (
	MetricsNamespace = "aim"
	MetricsSubsystem = "service"
)

// ---- Kafka topics ----

const (
	KafkaTopicMessageCreated          = "message.created"
	KafkaTopicMessageRecalled         = "message.recalled"
	KafkaTopicConversationReadUpdated = "conversation.read.updated"
	KafkaTopicMessageEdited           = "message.edited"
	KafkaTopicMessageDeleted          = "message.deleted"
	KafkaTopicBotReplyRequested       = "bot.reply.requested"
	KafkaTopicDocumentParseRequested  = "document.parse.requested"
	KafkaTopicConvBotAdded            = "conversation.bot.added"
	KafkaTopicConvBotRemoved          = "conversation.bot.removed"
	KafkaTopicBotEventAI              = "bot.event.ai"
	KafkaTopicConvMemberJoined        = "conversation.member.joined"
	KafkaTopicConvMemberLeft          = "conversation.member.left"
	KafkaTopicMessageCreatedDLQ       = "message.created.dlq"
)

// ---- Elasticsearch ----

const (
	ESIndexMessages = "messages_v2"
)

// ---- Redis key templates ----

const (
	CacheKeyUserDevices    = "user:%d:devices"
	CacheKeyUserDevice     = "user:%d:device:%s"
	CacheKeyPresenceSub    = "presence:sub:%d"
	CacheKeyConvSeq        = "conv:seq:%d"
	CacheKeyMsgIdempotent  = "msg:idempotent:%s"
	CacheKeyUserReadSeq    = "user:%d:device:%s:conv:%d:read_seq"
	CacheKeyTyping         = "typing:%d:%d"
	CacheKeyRefreshToken   = "refresh_token:%s"
	CacheKeyConvList       = "conv_list:%d"
	CacheKeyRateLimit      = "rate:%d:%s"
	CacheKeyLLMRPM         = "llm:rate:rpm:%s"
	CacheKeyLLMConcurrency = "llm:rate:conc:%s"
)

// ---- Error codes ----

const (
	ErrCodeUnknown  = 1000
	ErrCodeInternal = 1006
)

// ---- Database defaults ----

const (
	DBDriverPostgres  = "postgres"
	DBMaxOpenConn     = 100
	DBMaxIdleConn     = 10
	DBConnMaxLifetime = 3600
	DBDefaultLogLevel = "warn"
)

// ---- Redis defaults ----

const (
	RedisDefaultAddr     = "localhost:6379"
	RedisPoolSize        = 100
	RedisMinIdleConn     = 10
	RedisDialTimeoutSec  = 5
	RedisReadTimeoutSec  = 3
	RedisWriteTimeoutSec = 3
)

// ---- Snowflake parameters ----

const (
	SnowflakeEpoch         int64 = 1700000000000
	SnowflakeWorkerBits    uint8 = 10
	SnowflakeSequenceBits  uint8 = 12
	SnowflakeWorkerDefault       = 1
)

// ---- Message / Conversation limits ----

const (
	RecallWindowSec    = 120
	EditWindowSec      = 120
	MaxMemberCount     = 500
	MaxGroupNameLen    = 64
	MaxAliasLen        = 32
	MaxAnnouncementLen = 1024
)

// ---- File service ----

const (
	FileUploadPathPattern = "files/%s/%d%s"
	ThumbnailJPEGQuality  = 80
	PresignedURLExpiry    = 1 * time.Hour
	MinIOBucketDefault    = "aim"
	MinIOEndpointDefault  = "localhost:9000"
)

// ---- Logging defaults ----

const (
	LogDefaultLevel     = "info"
	LogDefaultFormat    = "json"
	LogDefaultOutput    = "stdout"
	LogDefaultMaxSize   = 100
	LogDefaultMaxBackup = 10
	LogDefaultMaxAge    = 30
)

// ---- Tracing defaults ----

const (
	TraceDefaultEndpoint = "localhost:4317"
	TraceDefaultSampler  = "always_on"
	TraceDefaultRatio    = 1.0
	TraceTracerName      = "aim"
)

// ---- Rate limit types ----

const (
	RateLimitPerSecond = "per_second"
	RateLimitPerMinute = "per_minute"
	RateLimitPerHour   = "per_hour"
)

// ---- Service config defaults ----

const (
	DefaultServiceHost = "0.0.0.0"
	DefaultServerMode  = "release"
)

// ---- Kafka defaults ----

const (
	KafkaDefaultVersion = "2.8.0"
)
