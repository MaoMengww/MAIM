package stream

import (
	"context"
	"testing"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSESender_SendAndRecv(t *testing.T) {
	s := NewSSESender()
	defer s.Close()

	chunk := &model.StreamChunk{
		Type:    "chunk",
		Content: "你好",
	}

	err := s.Send(chunk)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	received, err := s.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, "chunk", received.Type)
	assert.Equal(t, "你好", received.Content)
}

func TestSSESender_MultipleChunks(t *testing.T) {
	s := NewSSESender()
	defer s.Close()

	chunks := []*model.StreamChunk{
		{Type: "chunk", Content: "你"},
		{Type: "chunk", Content: "好"},
		{Type: "done", MessageID: "msg_123"},
	}

	for _, c := range chunks {
		err := s.Send(c)
		require.NoError(t, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	for _, expected := range chunks {
		received, err := s.Recv(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected.Type, received.Type)
		assert.Equal(t, expected.Content, received.Content)
	}
}

func TestSSESender_ContextCancel(t *testing.T) {
	s := NewSSESender()
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	_, err := s.Recv(ctx)
	assert.Error(t, err)
}

func TestSSESender_ToJSON(t *testing.T) {
	s := NewSSESender()
	defer s.Close()

	chunk := &model.StreamChunk{
		Type:      "done",
		Content:   "全文",
		MessageID: "msg_789",
		ConvID:    456,
	}

	b, err := s.ToJSON(chunk)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"type":"done"`)
	assert.Contains(t, string(b), `"content":"全文"`)
	assert.Contains(t, string(b), `"message_id":"msg_789"`)
}
