package consumer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/logx"
)

type CallbackClient struct {
	client *http.Client
	logger logx.Logger
}

func NewCallbackClient(logger logx.Logger) *CallbackClient {
	return &CallbackClient{
		client: &http.Client{Timeout: consts.WebhookCallbackTimeout * time.Second},
		logger: logger,
	}
}

func (c *CallbackClient) Send(ctx context.Context, callbackURL, webhookSecret string, payload json.RawMessage) error {
	signature, timestamp := signPayload(payload, webhookSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	req.Header.Set("Content-Type", consts.ContentTypeJSON)
	req.Header.Set(consts.HeaderAIMSignature, signature)
	req.Header.Set(consts.HeaderAIMTimestamp, fmt.Sprintf("%d", timestamp))
	// ... retry logic omitted, same as original
	var lastErr error
	backoff := time.Second
	for i := 0; i < consts.WebhookMaxRetries; i++ {
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook returned %d", resp.StatusCode)
		time.Sleep(backoff)
		backoff *= 2
		req.Body = io.NopCloser(bytes.NewReader(payload))
	}
	return lastErr
}

func signPayload(payload []byte, secret string) (signature string, timestamp int64) {
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), ts
}
