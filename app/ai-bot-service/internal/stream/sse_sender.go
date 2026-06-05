package stream

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
)

// SSESender sends streaming chunks via gRPC server stream (for single-chat SSE).
type SSESender struct {
	chunks chan *model.StreamChunk
	done   chan struct{}
}

// NewSSESender creates a new SSESender.
func NewSSESender() *SSESender {
	return &SSESender{
		chunks: make(chan *model.StreamChunk, 64),
		done:   make(chan struct{}),
	}
}

// Send pushes a chunk to the SSE client.
func (s *SSESender) Send(chunk *model.StreamChunk) error {
	select {
	case s.chunks <- chunk:
		return nil
	case <-s.done:
		return context.Canceled
	}
}

// Recv returns the next chunk. Used by the gRPC server to read and push to the stream.
func (s *SSESender) Recv(ctx context.Context) (*model.StreamChunk, error) {
	select {
	case chunk, ok := <-s.chunks:
		if !ok {
			return nil, nil
		}
		return chunk, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close signals the sender to stop.
func (s *SSESender) Close() {
	close(s.done)
	close(s.chunks)
}

// ToJSON serializes a chunk for SSE output.
func (s *SSESender) ToJSON(chunk *model.StreamChunk) ([]byte, error) {
	return json.Marshal(chunk)
}
