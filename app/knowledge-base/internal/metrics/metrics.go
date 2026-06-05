package metrics

import "github.com/maomeng/aim/pkg/metrics"

var (
	KbDocumentTotal = metrics.NewGaugeVec("kb_documents_total",
		"Total documents per knowledge base", "kb_id", "status")
	KbSearchTotal = metrics.NewCounterVec("kb_search_total",
		"Knowledge base search count", "mode")
	KbIngestDuration = metrics.NewHistogramVec("kb_ingest_duration_seconds",
		"Document ingestion duration", []string{"status"}, nil)
)
