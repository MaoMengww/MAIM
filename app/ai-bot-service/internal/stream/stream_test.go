package stream

import (
	"context"
	"testing"
	"time"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
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

func TestNewSSESender_NotNil(t *testing.T) {
	s := NewSSESender()
	assert.NotNil(t, s)
	assert.NotNil(t, s.chunks)
	assert.NotNil(t, s.done)
	s.Close()
}

func TestNewWSPusher(t *testing.T) {
	w := NewWSPusher(nil, 456, 1001, 789)
	assert.NotNil(t, w)
	assert.Equal(t, int64(456), w.convID)
	assert.Equal(t, int64(1001), w.botID)
	assert.Equal(t, int64(789), w.replyToMsgID)
	assert.Equal(t, int64(0), w.seq)
}

type mockWsClient struct {
	err     error
	lastMsg []byte
}

func (m *mockWsClient) StreamToConv(ctx context.Context, convID int64, msg []byte) error {
	m.lastMsg = msg
	return m.err
}

func TestWSPusher_SeqIncrement(t *testing.T) {
	mc := &mockWsClient{}
	w := NewWSPusher(mc, 456, 1001, 0)
	assert.Equal(t, int64(0), w.seq)

	err := w.Send(&model.StreamChunk{Type: "chunk", Content: "test"})
	assert.NoError(t, err)
	assert.Equal(t, int64(1), w.seq)
}

func TestWSPusher_ReplyToMsgIDInFirstChunk(t *testing.T) {
	mc := &mockWsClient{}
	w := NewWSPusher(mc, 456, 1001, 789)

	err := w.Send(&model.StreamChunk{Type: "chunk", Content: "hello"})
	assert.NoError(t, err)
	assert.Contains(t, string(mc.lastMsg), `"reply_to_msg_id":789`)
}

func TestWSPusher_ReplyToMsgIDOnlyInFirstChunk(t *testing.T) {
	mc := &mockWsClient{}
	w := NewWSPusher(mc, 456, 1001, 789)

	_ = w.Send(&model.StreamChunk{Type: "chunk", Content: "first"})
	_ = w.Send(&model.StreamChunk{Type: "chunk", Content: "second"})
	assert.NotContains(t, string(mc.lastMsg), "reply_to_msg_id")
}
