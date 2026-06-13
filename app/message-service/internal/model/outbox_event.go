package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// OutboxStatus values
const (
	OutboxStatusPending = 0 // waiting to be sent
	OutboxStatusSent    = 1 // successfully sent to Kafka
	OutboxStatusFailed  = 2 // exceeded max retries (DLQ)
)

// DefaultMaxRetries is the default maximum number of Kafka send retries.
const DefaultMaxRetries = 10

// JSONBytes is a raw JSON value stored as jsonb in PostgreSQL.
type JSONBytes []byte

func (j JSONBytes) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return []byte(j), nil
}

func (j *JSONBytes) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	*j = make([]byte, len(bytes))
	copy(*j, bytes)
	return nil
}

type OutboxEvent struct {
	ID           int64      `gorm:"primaryKey;column:id"`
	Topic        string     `gorm:"column:topic;size:64;not null"`
	Key          string     `gorm:"column:key;size:128;not null"`
	Payload      JSONBytes  `gorm:"column:payload;type:jsonb;not null"`
	Status       int16      `gorm:"column:status;not null;default:0"`
	RetryCount   int        `gorm:"column:retry_count;not null;default:0"`
	MaxRetries   int        `gorm:"column:max_retries;not null;default:10"`
	NextRetryAt  *time.Time `gorm:"column:next_retry_at"`
	LastError    string     `gorm:"column:last_error;type:text"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;autoCreateTime"`
	DispatchedAt *time.Time `gorm:"column:dispatched_at"`
}

func (OutboxEvent) TableName() string {
	return "msg.outbox_events"
}

// UnmarshalPayload unmarshals the JSONB payload into the given target.
func (e *OutboxEvent) UnmarshalPayload(v any) error {
	return json.Unmarshal(e.Payload, v)
}

// SetPayload marshals the given value into the JSONB payload.
func (e *OutboxEvent) SetPayload(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.Payload = JSONBytes(b)
	return nil
}
