package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	KafkaMessagesTotal = metrics.NewCounterVec("kafka_messages_total", "Total Kafka messages processed", "topic")
	KafkaProcessingSeconds = metrics.NewHistogramVec("kafka_processing_seconds", "Kafka message processing time in seconds", []string{"topic"}, nil)
)
