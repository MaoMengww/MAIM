package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esutil"

	"github.com/maomeng/aim/pkg/config"
)

type Client struct {
	client *elasticsearch.Client
}

func NewClient(cfg config.ElasticsearchConfig) (*Client, error) {
	if len(cfg.Addresses) == 0 {
		cfg.Addresses = []string{"http://localhost:9200"}
	}

	esCfg := elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		CloudID:   cfg.CloudID,
		APIKey:    cfg.APIKey,
	}

	es, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch client create failed: %w", err)
	}

	res, err := es.Ping()
	if err != nil {
		return nil, fmt.Errorf("elasticsearch ping failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch ping error: %s", res.String())
	}

	return &Client{client: es}, nil
}

func (c *Client) Raw() *elasticsearch.Client {
	return c.client
}

func (c *Client) Index(ctx context.Context, index string, id string, doc any) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal document failed: %w", err)
	}

	res, err := c.client.Index(index, bytes.NewReader(body), c.client.Index.WithDocumentID(id), c.client.Index.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("index document failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("index document error: %s", res.String())
	}
	return nil
}

func (c *Client) Get(ctx context.Context, index string, id string) ([]byte, error) {
	res, err := c.client.Get(index, id, c.client.Get.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("get document failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		if res.StatusCode == 404 {
			return nil, nil
		}
		return nil, fmt.Errorf("get document error: %s", res.String())
	}
	return io.ReadAll(res.Body)
}

func (c *Client) Search(ctx context.Context, index string, query map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return nil, fmt.Errorf("encode query failed: %w", err)
	}

	res, err := c.client.Search(
		c.client.Search.WithContext(ctx),
		c.client.Search.WithIndex(index),
		c.client.Search.WithBody(&buf),
	)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", res.String())
	}
	return io.ReadAll(res.Body)
}

func (c *Client) Delete(ctx context.Context, index string, id string) error {
	res, err := c.client.Delete(index, id, c.client.Delete.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("delete document failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("delete document error: %s", res.String())
	}
	return nil
}

func (c *Client) DeleteByQuery(ctx context.Context, index string, query map[string]any) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(query); err != nil {
		return fmt.Errorf("encode query failed: %w", err)
	}

	res, err := c.client.DeleteByQuery(
		[]string{index},
		bytes.NewReader(buf.Bytes()),
		c.client.DeleteByQuery.WithContext(ctx),
	)
	if err != nil {
		return fmt.Errorf("delete by query failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("delete by query error: %s", res.String())
	}
	return nil
}

func (c *Client) BulkIndex(ctx context.Context, index string, docs map[string]any) error {
	var firstErr error
	var mu sync.Mutex

	bulk, err := esutil.NewBulkIndexer(esutil.BulkIndexerConfig{
		Index:  index,
		Client: c.client,
		OnError: func(_ context.Context, err error) {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		},
	})
	if err != nil {
		return fmt.Errorf("create bulk indexer failed: %w", err)
	}

	for id, doc := range docs {
		data, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("marshal document %s failed: %w", id, err)
		}
		if err := bulk.Add(ctx, esutil.BulkIndexerItem{
			Action:     "index",
			DocumentID: id,
			Body:       bytes.NewReader(data),
		}); err != nil {
			return fmt.Errorf("bulk add %s failed: %w", id, err)
		}
	}

	if err := bulk.Close(ctx); err != nil {
		return fmt.Errorf("bulk close failed: %w", err)
	}

	if firstErr != nil {
		return fmt.Errorf("bulk index error: %w", firstErr)
	}

	return nil
}

func (c *Client) EnsureIndex(ctx context.Context, index string, mapping map[string]any) error {
	res, err := c.client.Indices.Exists([]string{index}, c.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("check index exists failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == 200 {
		return nil
	}

	var body io.Reader
	if mapping != nil {
		data, err := json.Marshal(mapping)
		if err != nil {
			return fmt.Errorf("marshal mapping failed: %w", err)
		}
		body = strings.NewReader(string(data))
	}

	res, err = c.client.Indices.Create(index, c.client.Indices.Create.WithBody(body), c.client.Indices.Create.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("create index failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("create index error: %s", res.String())
	}
	return nil
}

func (c *Client) Close() error {
	return nil
}
