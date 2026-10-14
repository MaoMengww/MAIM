package es

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MigrateBotContent removes the retired identity-as-content classification from
// existing search projections. PostgreSQL remains authoritative for result bodies.
func (c *Client) MigrateBotContent(ctx context.Context, index string) error {
	body := strings.NewReader(`{"query":{"term":{"msg_type":9}},"script":{"lang":"painless","source":"ctx._source.msg_type = 1; ctx._source.content = ['text':ctx._source.content.text,'mention_user_ids':[],'mention_all':false];"}}`)
	res, err := c.client.UpdateByQuery([]string{index}, c.client.UpdateByQuery.WithContext(ctx), c.client.UpdateByQuery.WithBody(body), c.client.UpdateByQuery.WithRefresh(true))
	if err != nil {
		return err
	}
	// Status and decoding determine migration success; closing releases the body.
	defer func() { _ = res.Body.Close() }()
	if res.IsError() {
		return fmt.Errorf("migrate bot search content: %s", res.String())
	}
	var result struct {
		TimedOut  bool              `json:"timed_out"`
		Conflicts int64             `json:"version_conflicts"`
		Failures  []json.RawMessage `json:"failures"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return err
	}
	if result.TimedOut || result.Conflicts != 0 || len(result.Failures) != 0 {
		return fmt.Errorf("bot search migration incomplete: timeout=%t conflicts=%d failures=%d", result.TimedOut, result.Conflicts, len(result.Failures))
	}
	return nil
}
