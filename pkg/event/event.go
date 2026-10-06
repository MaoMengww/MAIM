package event

import (
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
)

type EventType string

const (
	EventTypeKnowledgeParsing   EventType = "knowledge.parsing"
	EventTypeKnowledgeChunking  EventType = "knowledge.chunking"
	EventTypeKnowledgeEmbedding EventType = "knowledge.embedding"
	EventTypeKnowledgeReady     EventType = "knowledge.ready"
	EventTypeKnowledgeFailed    EventType = "knowledge.failed"
)

type EventLevel string

const (
	EventLevelInfo    EventLevel = "info"
	EventLevelSuccess EventLevel = "success"
	EventLevelWarning EventLevel = "warning"
	EventLevelError   EventLevel = "error"
)

type RealtimeEvent struct {
	Type      EventType      `json:"type"`
	Level     EventLevel     `json:"level"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Source    string         `json:"source,omitempty"`
	UserID    *string        `json:"user_id,omitempty"`
	ConvID    *string        `json:"conv_id,omitempty"`
	DocID     *string        `json:"doc_id,omitempty"`
	KBID      *string        `json:"kb_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt int64          `json:"created_at"`
}

func MarshalRealtimeEvent(evt RealtimeEvent) ([]byte, error) {
	return json.Marshal(evt)
}

// InboxChangeEvent carries all durable changes on the conversation-keyed channel.
type InboxChangeEvent struct {
	ChangeID            string         `json:"change_id"`
	Kind                string         `json:"kind"`
	RecipientIDs        []string       `json:"recipient_ids,omitempty"`
	UserID              *string        `json:"user_id,omitempty"`
	LastReadSeq         int64          `json:"last_read_seq,omitzero"`
	DeleteForAll        bool           `json:"delete_for_all,omitzero"`
	MessageID           *string        `json:"message_id,omitempty"`
	ConvID              string         `json:"conv_id"`
	SenderID            *string        `json:"sender_id,omitempty"`
	SenderType          string         `json:"sender_type"`
	MsgType             int32          `json:"msg_type"`
	Content             map[string]any `json:"content"`
	Seq                 int64          `json:"seq"`
	PublicationSequence int64          `json:"publication_sequence"`
	ReplyToMsgID        *string        `json:"reply_to_msg_id,omitempty"`
	CreatedAt           int64          `json:"created_at"`
}

// WebhookPayload is the JSON body sent to third-party webhook callback URLs.
type WebhookPayload struct {
	EventType    string         `json:"event_type"`
	EventID      string         `json:"event_id"`
	BotID        string         `json:"bot_id"`
	ConvID       string         `json:"conv_id"`
	Message      map[string]any `json:"message,omitempty"`
	Sender       map[string]any `json:"sender,omitempty"`
	Conversation map[string]any `json:"conversation,omitempty"`
}

func (evt RealtimeEvent) Validate() error {
	for _, id := range []*string{evt.UserID, evt.ConvID, evt.DocID, evt.KBID} {
		if id != nil {
			if err := identity.Validate(*id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (evt InboxChangeEvent) Validate() error {
	for _, id := range []string{evt.ChangeID, evt.ConvID} {
		if err := identity.Validate(id); err != nil {
			return err
		}
	}
	for _, id := range evt.RecipientIDs {
		if err := identity.Validate(id); err != nil {
			return err
		}
	}
	for _, id := range []*string{evt.UserID, evt.MessageID, evt.SenderID, evt.ReplyToMsgID} {
		if id != nil {
			if err := identity.Validate(*id); err != nil {
				return err
			}
		}
	}
	if err := sequence.Validate(evt.Seq); err != nil {
		return fmt.Errorf("seq: %w", err)
	}
	if err := sequence.Validate(evt.LastReadSeq); err != nil {
		return fmt.Errorf("last_read_seq: %w", err)
	}
	if err := sequence.Validate(evt.PublicationSequence); err != nil {
		return fmt.Errorf("publication_sequence: %w", err)
	}
	return nil
}

func (evt InboxChangeEvent) MarshalJSON() ([]byte, error) {
	if err := evt.Validate(); err != nil {
		return nil, err
	}
	type payload InboxChangeEvent
	return json.Marshal(payload(evt))
}

func (evt *InboxChangeEvent) UnmarshalJSON(raw []byte) error {
	type payload InboxChangeEvent
	var next InboxChangeEvent
	decoded := struct {
		*payload
		Seq                 json.RawMessage `json:"seq"`
		LastReadSeq         json.RawMessage `json:"last_read_seq"`
		PublicationSequence json.RawMessage `json:"publication_sequence"`
	}{payload: (*payload)(&next)}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
		dst  *int64
	}{
		{"seq", decoded.Seq, &next.Seq},
		{"last_read_seq", decoded.LastReadSeq, &next.LastReadSeq},
		{"publication_sequence", decoded.PublicationSequence, &next.PublicationSequence},
	} {
		if len(field.raw) == 0 {
			continue
		}
		value, err := sequence.ParseJSON(field.raw)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		*field.dst = value
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*evt = next
	return nil
}

func (evt RealtimeEvent) MarshalJSON() ([]byte, error) {
	if err := evt.Validate(); err != nil {
		return nil, err
	}
	type payload RealtimeEvent
	return json.Marshal(payload(evt))
}

func (evt *RealtimeEvent) UnmarshalJSON(raw []byte) error {
	type payload RealtimeEvent
	var decoded payload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	next := RealtimeEvent(decoded)
	if err := next.Validate(); err != nil {
		return err
	}
	*evt = next
	return nil
}
