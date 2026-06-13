package consts

import "time"

// ---- Retry & backoff ----

const (
	RetryMaxAttempts   = 3
	RetryBaseBackoffMs = 100
)

// ---- JWT / Token ----

const (
	JWTDefaultExpireSec  = 3600
	JWTDefaultRefreshSec = 2592000 // 30 days
	MsgIdempotentTTL     = 7 * 24 * time.Hour
)

// ---- Webhook ----

const (
	WebhookCallbackTimeout = 30 // seconds, used as time.Duration multiplier
	WebhookMaxRetries      = 3
)

// ---- Redis key templates ----

const (
	CacheKeyUserDevices    = "user:%d:devices"
	CacheKeyTyping         = "typing:%d:%d"
	CacheKeyLLMConcurrency = "llm:rate:conc:%s"
)
