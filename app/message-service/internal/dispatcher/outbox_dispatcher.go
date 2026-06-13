package dispatcher

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
)

const (
	defaultBatchSize  = 100
	defaultInterval   = 100 * time.Millisecond
	maxBackoff        = 60 * time.Second
	cleanupInterval   = 6 * time.Hour
	cleanupRetention  = 7 * 24 * time.Hour
	cleanupBatchLimit = 10000
)

// OutboxDispatcher polls the outbox_events table and sends pending events to Kafka.
type OutboxDispatcher struct {
	outboxRepo *repo.OutboxRepo
	producers  map[string]*kafka.Producer
	logger     logx.Logger
	batchSize  int
	interval   time.Duration
}

func NewOutboxDispatcher(
	outboxRepo *repo.OutboxRepo,
	producers map[string]*kafka.Producer,
	logger logx.Logger,
) *OutboxDispatcher {
	return &OutboxDispatcher{
		outboxRepo: outboxRepo,
		producers:  producers,
		logger:     logger,
		batchSize:  defaultBatchSize,
		interval:   defaultInterval,
	}
}

// Run starts the main dispatch loop. It blocks until ctx is cancelled.
func (d *OutboxDispatcher) Run(ctx context.Context) {
	dispatchTicker := time.NewTicker(d.interval)
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer dispatchTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-dispatchTicker.C:
			d.dispatchBatch(ctx)
		case <-cleanupTicker.C:
			d.cleanupExpired(ctx)
		}
	}
}

func (d *OutboxDispatcher) dispatchBatch(ctx context.Context) {
	events, err := d.outboxRepo.FetchPending(ctx, d.batchSize)
	if err != nil {
		d.logger.WithContext(ctx).Errorf("outbox dispatcher fetch failed: %v", err)
		return
	}

	for _, evt := range events {
		producer, ok := d.producers[evt.Topic]
		if !ok {
			d.logger.WithContext(ctx).Errorf("outbox dispatcher: no producer for topic %s", evt.Topic)
			_ = d.outboxRepo.MarkFailed(ctx, evt.ID, fmt.Sprintf("no producer for topic %s", evt.Topic))
			metrics.OutboxFailedCount.Set(1, evt.Topic)
			continue
		}

		if err := producer.Send(ctx, evt.Key, []byte(evt.Payload)); err != nil {
			metrics.OutboxSendFailureTotal.Inc(evt.Topic)
			if evt.RetryCount+1 >= evt.MaxRetries {
				_ = d.outboxRepo.MarkFailed(ctx, evt.ID, err.Error())
				metrics.OutboxFailedCount.Set(1, evt.Topic)
				d.logger.WithContext(ctx).Errorf("outbox dispatcher: event %d exhausted retries, marked failed: %v", evt.ID, err)
			} else {
				nextRetry := time.Now().Add(exponentialBackoff(evt.RetryCount + 1))
				_ = d.outboxRepo.MarkRetry(ctx, evt.ID, nextRetry, err.Error())
			}
		} else {
			metrics.OutboxSendSuccessTotal.Inc(evt.Topic)
			_ = d.outboxRepo.MarkSent(ctx, evt.ID)
			latency := time.Since(evt.CreatedAt).Seconds()
			metrics.OutboxDispatchLatencySeconds.Observe(latency, evt.Topic)
		}
	}
}

func (d *OutboxDispatcher) cleanupExpired(ctx context.Context) {
	before := time.Now().Add(-cleanupRetention)
	deleted, err := d.outboxRepo.DeleteSentBefore(ctx, before, cleanupBatchLimit)
	if err != nil {
		d.logger.WithContext(ctx).Errorf("outbox dispatcher cleanup failed: %v", err)
		return
	}
	if deleted > 0 {
		d.logger.WithContext(ctx).Infof("outbox dispatcher cleaned up %d expired events", deleted)
	}
}

// exponentialBackoff returns the backoff duration for a given retry attempt.
// Sequence: 1s, 2s, 4s, 8s, 16s, 32s, 60s (capped).
func exponentialBackoff(retryCount int) time.Duration {
	d := time.Duration(math.Pow(2, float64(retryCount-1))) * time.Second
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}
