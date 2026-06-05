package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/net/http2"
)

type APNSSender struct {
	client     *http.Client
	keyID      string
	teamID     string
	privateKey *ecdsa.PrivateKey
}

func NewAPNSSender(keyFile, keyID, teamID string) (*APNSSender, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read apns key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid apns pem")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse apns key: %w", err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("apns key is not ecdsa")
	}
	return &APNSSender{
		client:     &http.Client{Transport: &http2.Transport{}},
		keyID:      keyID,
		teamID:     teamID,
		privateKey: ecKey,
	}, nil
}

func (s *APNSSender) Send(ctx context.Context, token, topic, title, body string, data map[string]string) error {
	isDev := os.Getenv("APNS_ENV") == "dev"
	host := "api.push.apple.com"
	if isDev {
		host = "api.sandbox.push.apple.com"
	}
	url := fmt.Sprintf("https://%s/3/device/%s", host, token)

	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]any{
				"title": title,
				"body":  body,
			},
			"sound": "default",
		},
	}
	for k, v := range data {
		payload[k] = v
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("apns-topic", topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-expiration", "0")

	bearer, err := s.generateJWT()
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+bearer)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("apns %d: %s", resp.StatusCode, string(respBody))
}

func (s *APNSSender) generateJWT() (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": s.teamID,
		"iat": time.Now().Unix(),
	})
	token.Header["kid"] = s.keyID
	return token.SignedString(s.privateKey)
}

func (s *APNSSender) SendToMany(ctx context.Context, tokens []string, topic, title, body string, data map[string]string) []error {
	errs := make([]error, 0)
	for _, token := range tokens {
		if err := s.Send(ctx, token, topic, title, body, data); err != nil {
			errs = append(errs, fmt.Errorf("token %s: %w", strconv.Quote(token[:8]+"..."), err))
		}
	}
	return errs
}
