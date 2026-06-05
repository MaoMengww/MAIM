package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	// 已有
	BotRequestsTotal = metrics.NewCounterVec("bot_requests_total",
		"Total bot requests", "bot_id", "status")
	BotResponseSeconds = metrics.NewHistogramVec("bot_response_seconds",
		"Bot response time in seconds", []string{"bot_id"}, nil)

	// 新增
	BotReplyMessagesTotal = metrics.NewCounterVec("bot_reply_messages_total",
		"Bot reply messages sent", "bot_id")
	BotToolCallTotal = metrics.NewCounterVec("bot_tool_calls_total",
		"Bot tool call count", "bot_id", "tool_name")
	BotMemoryOpsTotal = metrics.NewCounterVec("bot_memory_ops_total",
		"Bot memory operations", "bot_id", "op")
	BotLLMTokenTotal = metrics.NewCounterVec("bot_llm_tokens_total",
		"LLM token consumption by bot", "bot_id", "model", "type")
)
