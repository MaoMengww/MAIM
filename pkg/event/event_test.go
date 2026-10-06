package event

import (
	"encoding/json"
	"testing"

	"github.com/maomeng/aim/pkg/sequence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealtimeEventSSEPayload(t *testing.T) {
	id := "01963d97-bb37-7bea-a746-727bcc312b6f"
	evt := RealtimeEvent{Type: EventTypeKnowledgeFailed, DocID: &id}
	data, err := MarshalRealtimeEvent(evt)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.JSONEq(t, `"`+id+`"`, string(payload["doc_id"]))
	assert.NotContains(t, payload, "conv_id")
	invalid := "0"
	evt.ConvID = &invalid
	_, err = MarshalRealtimeEvent(evt)
	require.Error(t, err)
}

func TestInboxChangeEventSequenceAndPresence(t *testing.T) {
	id := "01963d97-bb37-7bea-a746-727bcc312b6f"
	evt := InboxChangeEvent{ChangeID: id, ConvID: id, Seq: sequence.Max, PublicationSequence: sequence.Max}
	data, err := json.Marshal(evt)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, "9007199254740991", string(payload["seq"]))
	assert.Equal(t, "9007199254740991", string(payload["publication_sequence"]))
	assert.NotContains(t, payload, "message_id")
	assert.NotContains(t, payload, "reply_to_msg_id")
	var decoded InboxChangeEvent
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, sequence.Max, decoded.Seq)
	assert.Nil(t, decoded.ReplyToMsgID)

	for _, invalid := range []json.RawMessage{json.RawMessage(`"1"`), json.RawMessage(`null`), json.RawMessage(`-1`), json.RawMessage(`0.5`), json.RawMessage(`9007199254740992`)} {
		for _, field := range []string{"seq", "last_read_seq", "publication_sequence"} {
			t.Run(field+"/"+string(invalid), func(t *testing.T) {
				payload[field] = invalid
				data, err := json.Marshal(payload)
				require.NoError(t, err)
				require.Error(t, json.Unmarshal(data, &decoded))
				delete(payload, field)
			})
		}
	}
}
