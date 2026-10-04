package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
