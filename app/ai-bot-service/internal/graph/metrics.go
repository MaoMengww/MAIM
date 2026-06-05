package graph

import (
	"github.com/zeromicro/go-zero/core/metric"
)

// Prometheus metric names for ai-bot-service.
const (
	metricBotRequestTotal    = "bot_request_total"
	metricBotTokenUsageTotal = "bot_token_usage_total"
	metricBotToolCallTotal   = "bot_tool_call_total"
	metricBotRequestDuration = "bot_request_duration_seconds"
	metricBotLLMDuration     = "bot_llm_duration_seconds"
	metricBotActiveRequests  = "bot_active_requests"
)

// MetricsCollector tracks Prometheus metrics for bot operations.
type MetricsCollector struct {
	requestTotal    *metric.CounterVec
	tokenUsage      *metric.CounterVec
	toolCallTotal   *metric.CounterVec
	requestDuration *metric.HistogramVec
	llmDuration     *metric.HistogramVec
	activeRequests  *metric.GaugeVec
}

// NewMetricsCollector creates and registers Prometheus metrics.
func NewMetricsCollector() *MetricsCollector {
	// TODO: Register with Prometheus when go-zero metric registration is wired.
	return &MetricsCollector{}
}

// RecordRequest records a completed bot request.
func (m *MetricsCollector) RecordRequest(botID int64, status string, duration float64) {
	// TODO: Increment request total and observe duration.
}

// RecordTokenUsage records LLM token consumption.
func (m *MetricsCollector) RecordTokenUsage(botID int64, model string, direction string, tokens int) {
	// TODO: Increment token usage counter.
}

// RecordToolCall records a tool call outcome.
func (m *MetricsCollector) RecordToolCall(botID int64, toolName string, success bool) {
	// TODO: Increment tool call counter.
}

// SetActiveRequests sets the active requests gauge.
func (m *MetricsCollector) SetActiveRequests(botID int64, count float64) {
	// TODO: Set gauge.
}
