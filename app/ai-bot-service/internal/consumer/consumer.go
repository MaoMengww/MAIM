package consumer

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/logx"
)

// Consumer manages Kafka consumption for bot.event.ai.
type Consumer struct {
	handler *Handler
	config  config.KafkaConfig
	logger  logx.Logger
}

// NewConsumer creates a new Consumer.
func NewConsumer(cfg config.KafkaConfig, handler *Handler, logger logx.Logger) *Consumer {
	return &Consumer{
		handler: handler,
		config:  cfg,
		logger:  logger,
	}
}

// Start begins consuming messages from bot.event.ai.
// Each partition gets a dedicated goroutine.
// TODO: Replace with actual Sarama consumer group when Kafka infrastructure is ready.
func (c *Consumer) Start(ctx context.Context) error {
	logger := c.logger.WithContext(context.Background())
	logger.Infof("kafka consumer starting for topic: %s", consts.KafkaTopicBotEventAI)

	// Placeholder: In production, create a Sarama consumer group here.
	// For now, set up graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		logger.Infof("kafka consumer stopped via context")
	case sig := <-sigCh:
		logger.Infof("kafka consumer stopped via signal: %v", sig)
	}
	return nil
}

// Stop shuts down the consumer gracefully.
func (c *Consumer) Stop() error {
	logger := c.logger.WithContext(context.Background())
	logger.Infof("kafka consumer shutting down")
	return nil
}

// HandleMessage is the entry point for processing a raw Kafka message.
func (c *Consumer) HandleMessage(ctx context.Context, key, value []byte) error {
	return c.handler.Handle(ctx, value)
}
