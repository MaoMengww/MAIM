package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/logx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	headerRequestID = "x-request-id"
	tracerName      = "kafka-consumer"
)

// ---- Trace Context Propagation ----

// saramaHeaderCarrier implements propagation.TextMapCarrier using a map.
type saramaHeaderCarrier map[string]string

func (c saramaHeaderCarrier) Get(key string) string { return c[key] }
func (c saramaHeaderCarrier) Set(key, value string) { c[key] = value }
func (c saramaHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// injectTraceContext injects OpenTelemetry trace context and request_id from ctx
// into sarama producer message headers.
func injectTraceContext(ctx context.Context, msg *sarama.ProducerMessage) {
	carrier := make(saramaHeaderCarrier)
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for k, v := range carrier {
		msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte(k), Value: []byte(v)})
	}
	if requestID, ok := ctx.Value(interceptor.ContextKeyRequestID).(string); ok && requestID != "" {
		msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte(headerRequestID), Value: []byte(requestID)})
	}
}

// ExtractTraceContext extracts OpenTelemetry trace context and request_id from
// sarama consumer message headers, returning a child context of ctx.
func ExtractTraceContext(ctx context.Context, headers []*sarama.RecordHeader) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	carrier := make(saramaHeaderCarrier, len(headers))
	for _, h := range headers {
		carrier[string(h.Key)] = string(h.Value)
	}
	ctx = otel.GetTextMapPropagator().Extract(ctx, carrier)
	for _, h := range headers {
		if string(h.Key) == headerRequestID {
			ctx = context.WithValue(ctx, interceptor.ContextKeyRequestID, string(h.Value))
			break
		}
	}
	return ctx
}

// ---- Producer ----

type Producer struct {
	sarama.SyncProducer
	topic  string
	logger logx.Logger
}

func NewTestProducer(sp sarama.SyncProducer) *Producer {
	return &Producer{SyncProducer: sp, topic: "test", logger: logx.DefaultLogger()}
}

func NewProducer(cfg config.KafkaConfig, topic string, log logx.Logger) (*Producer, error) {
	saramaCfg := sarama.NewConfig()
	saramaCfg.Producer.RequiredAcks = sarama.WaitForAll
	saramaCfg.Producer.Retry.Max = cfg.MaxRetry
	saramaCfg.Producer.Return.Successes = true

	if cfg.SASLEnable {
		saramaCfg.Net.SASL.Enable = true
		saramaCfg.Net.SASL.User = cfg.SASLUser
		saramaCfg.Net.SASL.Password = cfg.SASLPassword
	}

	producer, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		return nil, errors.Wrap(errors.CodeMQError, "kafka producer create failed", err)
	}

	return &Producer{SyncProducer: producer, topic: topic, logger: log}, nil
}

// NewTransactionalProducer creates a Kafka producer with exactly-once semantics via
// Kafka transactions. Use for topics where PG + Kafka write atomicity is required.
// Callers must use BeginTxn/Send/CommitTxn pattern (see Producer.SendTxn).
func NewTransactionalProducer(cfg config.KafkaConfig, transactionalID, topic string, log logx.Logger) (*Producer, error) {
	saramaCfg := sarama.NewConfig()
	saramaCfg.Producer.RequiredAcks = sarama.WaitForAll
	saramaCfg.Producer.Retry.Max = cfg.MaxRetry
	saramaCfg.Producer.Return.Successes = true
	saramaCfg.Producer.Idempotent = true
	saramaCfg.Net.MaxOpenRequests = 1
	saramaCfg.Producer.Transaction.ID = transactionalID

	if cfg.SASLEnable {
		saramaCfg.Net.SASL.Enable = true
		saramaCfg.Net.SASL.User = cfg.SASLUser
		saramaCfg.Net.SASL.Password = cfg.SASLPassword
	}

	producer, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		return nil, errors.Wrap(errors.CodeMQError, "kafka transactional producer create failed", err)
	}

	return &Producer{SyncProducer: producer, topic: topic, logger: log}, nil
}

func (p *Producer) Send(ctx context.Context, key string, value []byte) error {
	msg := &sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(value),
	}
	injectTraceContext(ctx, msg)

	_, _, err := p.SyncProducer.SendMessage(msg)
	if err != nil {
		p.logger.WithContext(ctx).Errorf("kafka send failed: key=%s, err=%v", key, err)
		return errors.Wrap(errors.CodeMQError, "kafka send failed", err)
	}
	return nil
}

func (p *Producer) Close() error {
	return p.SyncProducer.Close()
}

// ---- Consumer ----

type Consumer struct {
	group   sarama.ConsumerGroup
	client  sarama.Client
	topics  []string
	handler sarama.ConsumerGroupHandler
	logger  logx.Logger
}

func NewConsumer(cfg config.KafkaConfig, topics []string, groupID string, log logx.Logger) (*Consumer, error) {
	saramaCfg := sarama.NewConfig()
	saramaCfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	saramaCfg.Consumer.IsolationLevel = sarama.ReadCommitted

	if cfg.SASLEnable {
		saramaCfg.Net.SASL.Enable = true
		saramaCfg.Net.SASL.User = cfg.SASLUser
		saramaCfg.Net.SASL.Password = cfg.SASLPassword
	}

	group, err := sarama.NewConsumerGroup(cfg.Brokers, groupID, saramaCfg)
	if err != nil {
		return nil, errors.Wrap(errors.CodeMQError, "kafka consumer create failed", err)
	}

	// Create a separate client for offset queries (e.g. consumer lag monitoring).
	client, err := sarama.NewClient(cfg.Brokers, saramaCfg)
	if err != nil {
		return nil, errors.Wrap(errors.CodeMQError, "kafka client create failed", err)
	}

	return &Consumer{group: group, client: client, topics: topics, logger: log}, nil
}

func (c *Consumer) Consume(ctx context.Context, handler sarama.ConsumerGroupHandler) error {
	c.handler = handler
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := c.group.Consume(ctx, c.topics, handler); err != nil {
				c.logger.WithContext(ctx).Errorf("kafka consume error: %v", err)
				return errors.Wrap(errors.CodeMQError, "kafka consume failed", err)
			}
		}
	}
}

// GetClient returns the underlying sarama.Client for offset queries.
func (c *Consumer) GetClient() sarama.Client {
	return c.client
}

func (c *Consumer) Close() error {
	if err := c.group.Close(); err != nil {
		return err
	}
	return c.client.Close()
}

// ---- Generic MessageHandler ----

type MessageHandler struct {
	OnMessage func(ctx context.Context, key, value []byte) error
	Logger    logx.Logger
}

func (h *MessageHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *MessageHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *MessageHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		ctx := ExtractTraceContext(sess.Context(), msg.Headers)
		tr := otel.Tracer(tracerName)
		topic := claim.Topic()
		ctx, span := tr.Start(ctx, fmt.Sprintf("consume %s", topic),
			trace.WithSpanKind(trace.SpanKindConsumer))
		defer span.End()
		if err := h.OnMessage(ctx, msg.Key, msg.Value); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			h.Logger.WithContext(ctx).Errorf("kafka message handler error: %v", err)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		sess.MarkMessage(msg, "")
	}
	return nil
}

// ---- Helpers ----

func RetrySend(producer *Producer, ctx context.Context, key string, value []byte, maxRetries int) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := producer.Send(ctx, key, value); err != nil {
			lastErr = err
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
			continue
		}
		return nil
	}
	return fmt.Errorf("send failed after %d retries: %w", maxRetries, lastErr)
}
