package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	// OutboxPendingCount is the current number of pending outbox events by topic.
	OutboxPendingCount = metrics.NewGaugeVec("outbox_pending_count",
		"Current number of pending outbox events", "topic")

	// OutboxSendSuccessTotal is the total number of successfully dispatched events by topic.
	OutboxSendSuccessTotal = metrics.NewCounterVec("outbox_send_success_total",
		"Total successfully dispatched outbox events", "topic")

	// OutboxSendFailureTotal is the total number of failed dispatch attempts by topic.
	OutboxSendFailureTotal = metrics.NewCounterVec("outbox_send_failure_total",
		"Total failed outbox dispatch attempts", "topic")

	// OutboxFailedCount is the current number of events in failed (DLQ) state by topic.
	OutboxFailedCount = metrics.NewGaugeVec("outbox_failed_count",
		"Current number of outbox events in failed state", "topic")

	// OutboxDispatchLatencySeconds is the latency from event creation to dispatch completion.
	OutboxDispatchLatencySeconds = metrics.NewHistogramVec("outbox_dispatch_latency_seconds",
		"Dispatch latency in seconds from event creation to Kafka send", []string{"topic"}, nil)
)
