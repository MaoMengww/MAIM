package delivery

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
)

const Topic = "delivery.requested"

// Intent contains recipients resolved by the domain owning the event.
// Realtime resolves devices only; it never resolves conversation membership.
type Intent struct {
	UserIDs         []int64         `json:"user_ids,omitempty"`
	BotIDs          []int64         `json:"bot_ids,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Notification    *Notification   `json:"notification,omitempty"`
	ExcludeUserID   int64           `json:"exclude_user_id,omitzero"`
	ExcludeDeviceID string          `json:"exclude_device_id,omitempty"`
}

type Notification struct {
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data,omitempty"`
}

type Publisher struct {
	producer *kafka.Producer
}

func NewPublisher(cfg config.KafkaConfig, logger logx.Logger) (*Publisher, error) {
	producer, err := kafka.NewProducer(cfg, Topic, logger)
	if err != nil {
		return nil, err
	}
	return &Publisher{producer: producer}, nil
}

func (p *Publisher) Publish(ctx context.Context, key int64, intent Intent) error {
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	return p.producer.Send(ctx, strconv.FormatInt(key, 10), payload)
}

func (p *Publisher) Close() error { return p.producer.Close() }
