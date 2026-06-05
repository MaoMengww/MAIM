package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	LLMRequestsTotal = metrics.NewCounterVec("llm_requests_total",
		"Total LLM API requests", "model", "status")
	LLMRequestDuration = metrics.NewHistogramVec("llm_request_duration_seconds",
		"LLM API request latency", []string{"model", "streaming"}, nil)
	LLMPromptTokensTotal = metrics.NewCounterVec("llm_prompt_tokens_total",
		"Total prompt tokens consumed", "model")
	LLMCompletionTokensTotal = metrics.NewCounterVec("llm_completion_tokens_total",
		"Total completion tokens consumed", "model")
	LLMCostTotal = metrics.NewCounterVec("llm_cost_total",
		"Total LLM API cost (cents)", "model")
)
