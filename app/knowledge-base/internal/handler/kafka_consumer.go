package handler

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/metrics"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
)

// Each claim processes messages synchronously. Shared slots bound all partitions,
// and the Kafka session waits for in-flight work before committing offsets.
type DocumentUploadedHandler struct {
	DocRepo        domain.DocumentRepo
	IngestPipe     *pipeline.IngestPipeline
	Logger         logx.Logger
	Ready          atomic.Bool
	slots          chan struct{}
	limiter        *rate.Limiter
	embeddingToken string
}

type DocumentUploadedEvent struct {
	DocID string `json:"doc_id"`
}

func NewDocumentUploadedHandler(docs domain.DocumentRepo, pipe *pipeline.IngestPipeline, logger logx.Logger, cfg config.IngestConfig) *DocumentUploadedHandler {
	return &DocumentUploadedHandler{
		DocRepo: docs, IngestPipe: pipe, Logger: logger,
		slots: make(chan struct{}, cfg.Concurrency), limiter: rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), 1),
		embeddingToken: cfg.EmbeddingToken,
	}
}

func (h *DocumentUploadedHandler) Setup(sarama.ConsumerGroupSession) error {
	h.Ready.Store(true)
	metrics.KbIngestConsumerReady.Set(1)
	return nil
}

func (h *DocumentUploadedHandler) Cleanup(sarama.ConsumerGroupSession) error {
	h.Ready.Store(false)
	metrics.KbIngestConsumerReady.Set(0)
	return nil
}

func (h *DocumentUploadedHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-sess.Context().Done():
			return nil
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			ctx := kafka.ExtractTraceContext(sess.Context(), msg.Headers)
			for {
				err := h.Handle(ctx, msg.Key, msg.Value)
				if ctx.Err() != nil {
					return nil // Never acknowledge interrupted work.
				}
				if err == nil {
					sess.MarkMessage(msg, "")
					break
				}
				metrics.KbIngestConsumerErrors.Inc("document")
				h.Logger.WithContext(ctx).Errorf("ingest message failed, retaining offset: %v", err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(3 * time.Second):
				}
			}
		}
	}
}

func (h *DocumentUploadedHandler) Handle(ctx context.Context, key, value []byte) error {
	var event DocumentUploadedEvent
	logger := h.Logger.WithContext(ctx)
	if err := json.Unmarshal(value, &event); err != nil || identity.Validate(event.DocID) != nil || string(key) != event.DocID {
		metrics.KbIngestConsumerErrors.Inc("invalid_event")
		logger.Errorf("invalid document.uploaded event")
		return nil // Poison events cannot identify a document to process.
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := h.limiter.Wait(ctx); err != nil {
		return err
	}
	pipeCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	pipeCtx = metadata.AppendToOutgoingContext(pipeCtx, "x-aim-embedding-workload", "ingest", "x-aim-ingest-token", h.embeddingToken)
	doc, err := h.DocRepo.Get(pipeCtx, event.DocID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if doc.Status == domain.DocStatusReady || doc.Status == domain.DocStatusFailed {
		return nil
	}
	metrics.KbIngestActive.Add(1)
	defer metrics.KbIngestActive.Add(-1)
	logger.Infof("ingest started: doc_id=%s kb_id=%s", doc.ID, doc.KBID)
	if err := h.IngestPipe.Run(pipeCtx, doc); err != nil {
		logger.Errorf("ingest failed: doc_id=%s error=%v", doc.ID, err)
		// Run persists failures while holding the document lock. Retry the
		// Kafka message only to recover storage failures; a durable failed
		// document is skipped and stays retryable through RetryDocument.
		return err
	}
	return nil
}
