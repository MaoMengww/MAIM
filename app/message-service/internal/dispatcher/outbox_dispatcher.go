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
	err := d.outboxRepo.InTransaction(ctx, func(outbox *repo.OutboxRepo) error {
		for dispatched := 0; dispatched < d.batchSize; {
			events, err := outbox.FetchPending(ctx, d.batchSize-dispatched)
			if err != nil {
				return err
			}
			if len(events) == 0 {
				return nil
			}
			for _, evt := range events {
				dispatched++
				producer, ok := d.producers[evt.Topic]
				if !ok {
					if err := outbox.MarkFailed(ctx, evt.ID, fmt.Sprintf("no producer for topic %s", evt.Topic)); err != nil {
						return err
					}
					metrics.OutboxFailedCount.Set(1, evt.Topic)
					continue
				}
				if err := producer.Send(ctx, evt.Key, []byte(evt.Payload)); err != nil {
					metrics.OutboxSendFailureTotal.Inc(evt.Topic)
					if evt.RetryCount+1 >= evt.MaxRetries {
						if err := outbox.MarkFailed(ctx, evt.ID, err.Error()); err != nil {
							return err
						}
						metrics.OutboxFailedCount.Set(1, evt.Topic)
						d.logger.WithContext(ctx).Errorf("outbox event %s exhausted retries; ordered key %s is blocked: %v", evt.ID, evt.Key, err)
					} else if err := outbox.MarkRetry(ctx, evt.ID, time.Now().Add(exponentialBackoff(evt.RetryCount+1)), err.Error()); err != nil {
						return err
					}
				} else {
					if err := outbox.MarkSent(ctx, evt.ID); err != nil {
						return err
					}
					metrics.OutboxSendSuccessTotal.Inc(evt.Topic)
					metrics.OutboxDispatchLatencySeconds.Observe(time.Since(evt.CreatedAt).Seconds(), evt.Topic)
				}
			}
		}
		return nil
	})
	if err != nil {
		d.logger.WithContext(ctx).Errorf("outbox dispatch transaction failed: %v", err)
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
