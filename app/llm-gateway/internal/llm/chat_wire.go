package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
)

// chatWireBridge observes metadata the component cannot represent: all choices,
// exact usage presence, empty deltas and an explicit provider completion marker.
// It never builds a request or independently drives the provider stream.
// State is written by the component's reader goroutine and read only after Recv
// returns its terminal error (the stream pipe provides the synchronization).
type chatWireBridge struct {
	ctx           context.Context
	provider      string
	send          func(*pb.ChatStreamChunk) error
	start         time.Time
	firstToken    *int64
	finished      map[int32]bool
	responseBytes int
	done          bool
	err           error
}

func (b *chatWireBridge) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("provider transport failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		b.err = &HTTPError{StatusCode: resp.StatusCode}
		return nil, b.err
	}
	body := &chatWireBody{ReadCloser: resp.Body, reader: bufio.NewReader(resp.Body), bridge: b}
	body.stop = context.AfterFunc(req.Context(), func() { _ = body.closeRaw() })
	resp.Body = body
	return resp, nil
}

func (b *chatWireBridge) result(err error) error {
	if b.ctx.Err() != nil {
		return b.ctx.Err()
	}
	if b.err != nil {
		return b.err
	}
	if !errors.Is(err, io.EOF) {
		// SDK errors can contain the provider body, URL, or prompt. Never expose them.
		return fmt.Errorf("provider stream read failed")
	}
	if !b.done {
		return fmt.Errorf("provider stream ended before completion")
	}
	if len(b.finished) == 0 {
		return fmt.Errorf("provider returned no choices")
	}
	for _, finished := range b.finished {
		if !finished {
			return fmt.Errorf("provider completion has no finish reason")
		}
	}
	return nil
}

func (b *chatWireBridge) observe(line []byte) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	if !ok {
		return nil
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("[DONE]")) {
		b.done = true
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	var wire struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int32           `json:"index"`
			Delta        providerMessage `json:"delta"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage *providerUsage  `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("invalid provider stream payload")
	}
	if len(wire.Error) > 0 && !bytes.Equal(wire.Error, []byte("null")) {
		return fmt.Errorf("provider stream rejected")
	}
	out := &pb.ChatStreamChunk{Id: wire.ID, Model: wire.Model, Usage: wire.Usage.proto(b.provider)}
	for _, c := range wire.Choices {
		if _, exists := b.finished[c.Index]; !exists {
			b.finished[c.Index] = false
		}
		if c.FinishReason != "" {
			b.finished[c.Index] = true
		}
		if b.firstToken == nil && (c.Delta.Content != "" || c.Delta.ReasoningContent != "" || len(c.Delta.ToolCalls) > 0) {
			elapsed := time.Since(b.start).Milliseconds()
			b.firstToken = &elapsed
		}
		out.Choices = append(out.Choices, &pb.ChatStreamChunk_Choice{Index: c.Index, Delta: c.Delta.proto(), FinishReason: c.FinishReason})
	}
	out.FirstTokenLatencyMs = b.firstToken
	return b.send(out)
}

// Read observes one bounded line at a time without reading ahead of the SDK.
// A partial line at socket EOF cannot become explicit completion evidence.
type chatWireBody struct {
	io.ReadCloser
	reader    *bufio.Reader
	bridge    *chatWireBridge
	pending   []byte
	readErr   error
	stop      func() bool
	closeOnce sync.Once
	closeErr  error
}

func (r *chatWireBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.readErr != nil {
			return 0, r.readErr
		}
		var line []byte
		for {
			part, err := r.reader.ReadSlice('\n')
			if len(line)+len(part) > 4*1024*1024 {
				r.bridge.err = fmt.Errorf("provider stream frame too large")
				return 0, r.bridge.err
			}
			if len(line) == 0 && !errors.Is(err, bufio.ErrBufferFull) {
				line = part
			} else {
				line = append(line, part...)
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				r.readErr = err
				break
			}
		}
		if r.readErr != nil {
			return 0, r.readErr
		}
		data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
		if !ok || len(bytes.TrimSpace(data)) == 0 {
			// SSE comments, separators and empty data lines are keepalives, not
			// model output. Do not spend the SDK's empty-frame budget on them.
			continue
		}
		r.bridge.responseBytes += len(line)
		if r.bridge.responseBytes > maxProviderResponseBytes {
			r.bridge.err = fmt.Errorf("provider response too large")
			return 0, r.bridge.err
		}
		if err := r.bridge.observe(line); err != nil {
			r.bridge.err = err
			return 0, err
		}
		r.pending = line
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *chatWireBody) Close() error {
	r.stop()
	return r.closeRaw()
}

func (r *chatWireBody) closeRaw() error {
	r.closeOnce.Do(func() { r.closeErr = r.ReadCloser.Close() })
	return r.closeErr
}
