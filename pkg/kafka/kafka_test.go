package kafka

import (
	"context"
	"testing"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/stretchr/testify/assert"
)

func TestNewProducerInvalidBroker(t *testing.T) {
	cfg := config.KafkaConfig{
		Brokers:  []string{"invalid:99999"},
		MaxRetry: 1,
	}
	log := logx.DefaultLogger()
	_, err := NewProducer(cfg, "test-topic", log)
	assert.Error(t, err)
}

func TestNewConsumerInvalidBroker(t *testing.T) {
	cfg := config.KafkaConfig{
		Brokers:  []string{"invalid:99999"},
		MaxRetry: 1,
	}
	log := logx.DefaultLogger()
	_, err := NewConsumer(cfg, []string{"test-topic"}, "test-group", log)
	assert.Error(t, err)
}

func TestMessageHandler(t *testing.T) {
	called := false
	handler := &MessageHandler{
		OnMessage: func(ctx context.Context, key, value []byte) error {
			called = true
			return nil
		},
	}
	assert.NotNil(t, handler)
	assert.False(t, called)
}
