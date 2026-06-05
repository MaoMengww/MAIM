package event

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealtimeEventMarshal(t *testing.T) {
	evt := RealtimeEvent{
		Type:    EventTypeKnowledgeEmbedding,
		Level:   EventLevelInfo,
		Title:   "正在向量化",
		Message: "文档正在生成向量，请稍候",
		Source:  "knowledge-base",
		UserID:  10,
		KBID:    100,
		DocID:   200,
		Metadata: map[string]any{
			"stage": "embedding",
		},
		CreatedAt: 1715068800000,
	}

	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var parsed RealtimeEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, EventTypeKnowledgeEmbedding, parsed.Type)
	assert.Equal(t, EventLevelInfo, parsed.Level)
	assert.Equal(t, "正在向量化", parsed.Title)
	assert.Equal(t, int64(10), parsed.UserID)
	assert.Equal(t, int64(100), parsed.KBID)
	assert.Equal(t, int64(200), parsed.DocID)
}

func TestRealtimeEventSSEPayload(t *testing.T) {
	evt := RealtimeEvent{
		Type:      EventTypeKnowledgeFailed,
		Level:     EventLevelError,
		Title:     "处理失败",
		Message:   "文档解析失败",
		CreatedAt: 1715068800000,
	}

	data, err := MarshalRealtimeEvent(evt)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"type":"knowledge.failed",
		"level":"error",
		"title":"处理失败",
		"message":"文档解析失败",
		"created_at":1715068800000
	}`, string(data))
}

func TestMessageCreatedEventMarshal(t *testing.T) {
	evt := MessageCreatedEvent{
		MessageID:  123,
		ConvID:     456,
		SenderID:   10,
		SenderType: "user",
		MsgType:    1,
		Content:    map[string]any{"text": "hello"},
		Seq:        50,
		CreatedAt:  1715068800,
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var parsed MessageCreatedEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, int64(123), parsed.MessageID)
	assert.Equal(t, int64(456), parsed.ConvID)
	assert.Equal(t, int64(10), parsed.SenderID)
	assert.Equal(t, "user", parsed.SenderType)
	assert.Equal(t, int32(1), parsed.MsgType)
}

func TestBotAddedToConvEvent(t *testing.T) {
	evt := BotAddedToConvEvent{
		ConvID:  100,
		BotID:   200,
		AddedBy: 10,
		BotName: "小助手",
		BotType: "official",
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var parsed BotAddedToConvEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, int64(100), parsed.ConvID)
	assert.Equal(t, int64(200), parsed.BotID)
	assert.Equal(t, "小助手", parsed.BotName)
	assert.Equal(t, "official", parsed.BotType)
}

func TestBotRemovedFromConvEvent(t *testing.T) {
	evt := BotRemovedFromConvEvent{
		ConvID:    100,
		BotID:     200,
		RemovedBy: 10,
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var parsed BotRemovedFromConvEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, int64(100), parsed.ConvID)
	assert.Equal(t, int64(200), parsed.BotID)
}

func TestMemberJoinedEvent(t *testing.T) {
	evt := MemberJoinedEvent{
		ConvID:   100,
		UserIDs:  []int64{20, 30, 40},
		JoinedBy: 10,
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var parsed MemberJoinedEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, int64(100), parsed.ConvID)
	assert.Equal(t, []int64{20, 30, 40}, parsed.UserIDs)
}

func TestMemberLeftEvent(t *testing.T) {
	evt := MemberLeftEvent{
		ConvID:    100,
		UserIDs:   []int64{20},
		RemovedBy: 10,
	}
	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var parsed MemberLeftEvent
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, []int64{20}, parsed.UserIDs)
}

func TestWebhookPayload(t *testing.T) {
	payload := WebhookPayload{
		EventType: "message.created",
		EventID:   "evt_abc123",
		BotID:     "bot_001",
		ConvID:    "conv_456",
		Message: map[string]any{
			"message_id": int64(789),
			"text":       "@小助手 天气",
		},
		Sender: map[string]any{
			"user_id":  int64(1),
			"username": "张三",
		},
		Conversation: map[string]any{
			"type": "group",
			"name": "产品讨论组",
		},
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed WebhookPayload
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "message.created", parsed.EventType)
	assert.Equal(t, "evt_abc123", parsed.EventID)
	assert.Equal(t, "bot_001", parsed.BotID)
	assert.Contains(t, parsed.Conversation["type"], "group")
}

func TestMessageCreatedEventDefaultSenderType(t *testing.T) {
	evt := MessageCreatedEvent{
		MessageID: 1,
		ConvID:    2,
		SenderID:  3,
	}
	assert.Equal(t, "", evt.SenderType) // default zero value
}
