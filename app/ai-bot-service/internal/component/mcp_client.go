package component

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	einoMCP "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
)

// GetMCPServerTools fetches all MCP tools from the given server configs.
// Returns tools whose InvokableRun returns clean text (MCP result envelope stripped)
// so the LLM receives parseable content instead of protocol-level JSON.
func GetMCPServerTools(ctx context.Context, servers []model.MCPServerConfig) ([]tool.BaseTool, error) {
	var allTools []tool.BaseTool
	for _, srv := range servers {
		tools, err := getServerTools(ctx, srv)
		if err != nil {
			continue
		}
		for _, t := range tools {
			invokable, ok := t.(tool.InvokableTool)
			if !ok {
				allTools = append(allTools, t)
				continue
			}
			allTools = append(allTools, &textTool{inner: invokable})
		}
		break
	}
	return allTools, nil
}

// textTool wraps an InvokableTool to strip the MCP protocol envelope
// from tool results, returning clean text that the LLM can understand.
type textTool struct {
	inner tool.InvokableTool
}

func (w *textTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *textTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	result, err := w.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		return "", err
	}
	return extractToolText(result), nil
}

// extractToolText extracts text content from the MCP CallToolResult envelope.
// If the result isn't an MCP envelope, returns it unchanged.
func extractToolText(result string) string {
	var envelope struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(result), &envelope); err != nil || len(envelope.Content) == 0 {
		return result
	}
	var texts []string
	for _, c := range envelope.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) == 0 {
		return result
	}
	return strings.Join(texts, "\n")
}

func getServerTools(ctx context.Context, srv model.MCPServerConfig) ([]tool.BaseTool, error) {
	cli, err := newMCPClient(srv)
	if err != nil {
		return nil, fmt.Errorf("create mcp client: %w", err)
	}

	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("mcp start: %w", err)
	}

	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "aim-bot",
				Version: "1.0.0",
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("mcp init: %w", err)
	}

	return einoMCP.GetTools(ctx, &einoMCP.Config{Cli: cli})
}

func newMCPClient(srv model.MCPServerConfig) (*client.Client, error) {
	headers := make(map[string]string)
	if srv.AuthConfig != nil {
		if srv.AuthConfig.APIKey != "" {
			headers["X-API-Key"] = srv.AuthConfig.APIKey
		}
		if srv.AuthConfig.Token != "" {
			headers["Authorization"] = "Bearer " + srv.AuthConfig.Token
		}
	}

	switch srv.Transport {
	case "sse":
		var opts []transport.ClientOption
		if len(headers) > 0 {
			opts = append(opts, transport.WithHeaders(headers))
		}
		return client.NewSSEMCPClient(srv.URL, opts...)
	default:
		var opts []transport.StreamableHTTPCOption
		if len(headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(headers))
		}
		if srv.AdvancedConfig != nil && srv.AdvancedConfig.Timeout > 0 {
			opts = append(opts, transport.WithHTTPTimeout(time.Duration(srv.AdvancedConfig.Timeout)*time.Second))
		}
		return client.NewStreamableHttpClient(srv.URL, opts...)
	}
}
