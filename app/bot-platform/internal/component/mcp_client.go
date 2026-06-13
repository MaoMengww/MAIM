package component

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/pkg/consts"
)

// MCPClient interacts with an MCP (Model Context Protocol) server over HTTP.
type MCPClient struct {
	url     string
	headers map[string]string
	timeout time.Duration
	client  *http.Client
}

// NewMCPClient creates a new MCP HTTP client from an McpServer model.
func NewMCPClient(srv *model.McpServer) *MCPClient {
	timeout := 30 * time.Second
	if srv.AdvancedConfig != nil && srv.AdvancedConfig.Timeout > 0 {
		timeout = time.Duration(srv.AdvancedConfig.Timeout) * time.Second
	}

	headers := make(map[string]string)
	headers[consts.HeaderContentType] = consts.ContentTypeJSON
	headers["Accept"] = consts.ContentTypeJSON + ", text/event-stream"
	if srv.AuthConfig != nil {
		if srv.AuthConfig.APIKey != "" {
			headers["X-API-Key"] = srv.AuthConfig.APIKey
		}
		if srv.AuthConfig.Token != "" {
			headers[consts.HeaderToken] = "Bearer " + srv.AuthConfig.Token
		}
	}

	return &MCPClient{
		url:     srv.URL,
		headers: headers,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

// MCPToolDef represents a tool definition from an MCP server.
type MCPToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}

// MCPListToolsResponse is the JSON-RPC response for tools/list.
type MCPListToolsResponse struct {
	Tools []MCPToolDef `json:"tools"`
}

// ListTools discovers available tools from the MCP server.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/list",
		"id":      1,
	}

	var resp MCPListToolsResponse
	if err := c.doJSON(ctx, reqBody, &resp); err != nil {
		return nil, err
	}
	return resp.Tools, nil
}

// mcpJSONRPCResponse is the generic JSON-RPC response envelope.
type mcpJSONRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *MCPClient) doJSON(ctx context.Context, body, result any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mcp returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Handle SSE (text/event-stream) and JSON responses
	contentType := resp.Header.Get("Content-Type")
	var jsonBody []byte
	if strings.HasPrefix(contentType, "text/event-stream") {
		jsonBody = parseSSEData(respBody)
	} else {
		jsonBody = respBody
	}

	// Unmarshal JSON-RPC envelope
	var rpcResp mcpJSONRPCResponse
	if err := json.Unmarshal(jsonBody, &rpcResp); err != nil {
		return fmt.Errorf("unmarshal jsonrpc response: %w", err)
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("mcp error: code=%d message=%s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if rpcResp.Result != nil {
		return json.Unmarshal(rpcResp.Result, result)
	}

	return fmt.Errorf("empty mcp response")
}

// parseSSEData extracts JSON from SSE (text/event-stream) format.
// It handles both standard SSE and the MCP Streamable HTTP response format
// where JSON-RPC responses are delivered in "data:" lines.
func parseSSEData(body []byte) []byte {
	var dataLines []string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			data := strings.TrimSpace(trimmed[5:])
			if data != "" {
				dataLines = append(dataLines, data)
			}
		}
	}
	return []byte(strings.Join(dataLines, "\n"))
}
