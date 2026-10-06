package delivery

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
)

const Topic = "delivery.requested"

// Intent contains recipients resolved by the domain owning the event.
// Realtime resolves devices only; it never resolves conversation membership.
type Intent struct {
	UserIDs         []string        `json:"user_ids,omitempty"`
	BotIDs          []string        `json:"bot_ids,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Notification    *Notification   `json:"notification,omitempty"`
	ExcludeUserID   *string         `json:"exclude_user_id,omitempty"`
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

func (p *Publisher) Publish(ctx context.Context, key string, intent Intent) error {
	if err := identity.Validate(key); err != nil {
		return err
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	return p.producer.Send(ctx, key, payload)
}

func (p *Publisher) Close() error { return p.producer.Close() }

func (intent Intent) Validate() error {
	for _, ids := range [][]string{intent.UserIDs, intent.BotIDs} {
		for _, id := range ids {
			if err := identity.Validate(id); err != nil {
				return err
			}
		}
	}
	if intent.ExcludeUserID != nil {
		return identity.Validate(*intent.ExcludeUserID)
	}
	return nil
}

func (intent Intent) MarshalJSON() ([]byte, error) {
	if err := intent.Validate(); err != nil {
		return nil, err
	}
	type payload Intent
	return json.Marshal(payload(intent))
}

func (intent *Intent) UnmarshalJSON(raw []byte) error {
	type payload Intent
	var decoded payload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	next := Intent(decoded)
	if err := next.Validate(); err != nil {
		return err
	}
	*intent = next
	return nil
}
