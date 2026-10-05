package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	KbDocumentTotal = metrics.NewGaugeVec("kb_documents_total",
		"Total documents per knowledge base", "kb_id", "status")
	KbSearchTotal = metrics.NewCounterVec("kb_search_total",
		"Knowledge base search count", "mode")
	KbIngestDuration = metrics.NewHistogramVec("kb_ingest_duration_seconds",
		"Document ingestion duration", []string{"status"}, nil)
	KbIngestTotal = metrics.NewCounterVec("kb_ingest_total",
		"Completed document ingestion attempts", "status")
	KbIngestActive = metrics.NewGaugeVec("kb_ingest_active",
		"Active document ingestion tasks")
	KbIngestConsumerReady = metrics.NewGaugeVec("kb_ingest_consumer_ready",
		"Whether the ingestion consumer has an active Kafka session")
	KbIngestConsumerErrors = metrics.NewCounterVec("kb_ingest_consumer_errors_total",
		"Ingestion consumer failures", "kind")
)
