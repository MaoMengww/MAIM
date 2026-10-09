package component

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/maomeng/aim/app/bot-service/internal/component/builtin"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// RunTool is a credential-free, immutable definition. Authorization and secrets
// are resolved again at invocation; a snapshot is never a permanent permit.
type RunTool struct {
	ServerID       string          `json:"server_id"`
	Transport      string          `json:"transport"`
	URL            string          `json:"url"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Parameters     json.RawMessage `json:"parameters"`
	DefinitionHash string          `json:"definition_hash"`
}

func openRunMCP(ctx context.Context, srv model.McpServer) (*client.Client, error) {
	endpoint, err := url.Parse(srv.URL)
	if err != nil || endpoint.User != nil {
		return nil, fmt.Errorf("MCP endpoint credentials must use transient auth configuration")
	}
	for key := range endpoint.Query() {
		if slices.Contains([]string{"token", "api_key", "apikey", "access_token", "key", "secret"}, strings.ToLower(key)) {
			return nil, fmt.Errorf("MCP endpoint must not embed credentials")
		}
	}
	cli, err := newMCPClient(model.MCPServerConfig{Name: srv.Name, Transport: srv.Transport, URL: srv.URL, AuthConfig: srv.AuthConfig, AdvancedConfig: srv.AdvancedConfig})
	if err != nil {
		return nil, err
	}
	if err := cli.Start(ctx); err != nil {
		_ = cli.Close()
		return nil, err
	}
	_, err = cli.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, Capabilities: mcp.ClientCapabilities{}, ClientInfo: mcp.Implementation{Name: "aim-bot", Version: "run-v1"}}})
	if err != nil {
		_ = cli.Close()
		return nil, err
	}
	return cli, nil
}
func safeRunTool(t mcp.Tool) bool {
	if t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint {
		return false
	}
	if t.Annotations.ReadOnlyHint != nil {
		return *t.Annotations.ReadOnlyHint
	}
	// Known read/network operations only. Names that merely sound benign do not
	// authorize unknown operations, sends, modifications, purchases or deletes.
	return slices.Contains([]string{"fetch", "fetch_url", "read_url", "web_search", "search", "wikipedia_search"}, t.Name)
}

func BuiltinRunTools(names []string) ([]RunTool, error) {
	tools := make([]RunTool, 0)
	for _, name := range names {
		// The existing builtin registry contains only trusted network reads.
		if name != "web_search" {
			return nil, fmt.Errorf("builtin tool is not authorized")
		}
		base, err := builtin.Registry[name]()
		if err != nil {
			return nil, err
		}
		definition, err := builtinRunToolDefinition(context.Background(), base)
		if err != nil {
			return nil, err
		}
		tools = append(tools, definition)
	}
	return tools, nil
}
func builtinRunToolDefinition(ctx context.Context, base tool.BaseTool) (RunTool, error) {
	info, err := base.Info(ctx)
	if err != nil {
		return RunTool{}, err
	}
	schema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return RunTool{}, err
	}
	parameters, err := json.Marshal(schema)
	if err != nil {
		return RunTool{}, err
	}
	digest := sha256.Sum256(append([]byte(info.Name+"\x00"+info.Desc+"\x00"), parameters...))
	return RunTool{ServerID: "builtin", Name: info.Name, Description: info.Desc, Parameters: parameters, DefinitionHash: hex.EncodeToString(digest[:])}, nil
}
func CallBuiltinRunTool(ctx context.Context, frozen RunTool, arguments string) (string, error) {
	if frozen.ServerID != "builtin" || frozen.Name != "web_search" {
		return "", fmt.Errorf("unknown builtin tool")
	}
	base, err := builtin.Registry[frozen.Name]()
	if err != nil {
		return "", err
	}
	invokable, ok := base.(tool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("builtin tool is not invokable")
	}
	definition, err := builtinRunToolDefinition(ctx, base)
	if err != nil {
		return "", err
	}
	if definition.DefinitionHash != frozen.DefinitionHash {
		return "", fmt.Errorf("tool definition changed; a new Run is required")
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args == nil {
		return "", fmt.Errorf("tool arguments must be a JSON object")
	}
	return invokable.InvokableRun(ctx, arguments)
}
func runToolDefinition(server model.McpServer, t mcp.Tool) (RunTool, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return RunTool{}, err
	}
	if server.AuthConfig != nil {
		for _, secret := range []string{server.AuthConfig.APIKey, server.AuthConfig.Token} {
			if secret != "" && strings.Contains(string(raw), secret) {
				return RunTool{}, fmt.Errorf("MCP tool definition contains credentials")
			}
		}
	}
	var definition struct {
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		return RunTool{}, err
	}
	var canonical any
	if err := json.Unmarshal(definition.InputSchema, &canonical); err != nil {
		return RunTool{}, err
	}
	parameters, err := json.Marshal(canonical)
	if err != nil {
		return RunTool{}, err
	}
	hash := sha256.Sum256(append([]byte(t.Name+"\x00"+t.Description+"\x00"), parameters...))
	return RunTool{ServerID: server.ID, Transport: server.Transport, URL: server.URL, Name: t.Name, Description: t.Description, Parameters: parameters, DefinitionHash: hex.EncodeToString(hash[:])}, nil
}
func DiscoverRunTools(ctx context.Context, servers []model.McpServer) ([]RunTool, error) {
	tools := make([]RunTool, 0)
	seen := make(map[string]bool)
	for _, srv := range servers {
		cli, err := openRunMCP(ctx, srv)
		if err != nil {
			return nil, fmt.Errorf("MCP discovery unavailable: %w", err)
		}
		result, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
		_ = cli.Close()
		if err != nil {
			return nil, err
		}
		for _, t := range result.Tools {
			if !safeRunTool(t) {
				continue
			}
			if seen[t.Name] {
				return nil, fmt.Errorf("duplicate MCP tool name %q", t.Name)
			}
			snapshot, err := runToolDefinition(srv, t)
			if err != nil {
				return nil, err
			}
			tools = append(tools, snapshot)
			seen[t.Name] = true
		}
	}
	slices.SortFunc(tools, func(a, b RunTool) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return tools, nil
}
func CallRunTool(ctx context.Context, srv model.McpServer, frozen RunTool, arguments string) (string, error) {
	if !srv.Enabled || srv.ID != frozen.ServerID || srv.Transport != frozen.Transport || srv.URL != frozen.URL {
		return "", fmt.Errorf("MCP access or routing changed")
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil || args == nil {
		return "", fmt.Errorf("tool arguments must be a JSON object")
	}
	cli, err := openRunMCP(ctx, srv)
	if err != nil {
		return "", err
	}
	defer cli.Close()
	listed, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return "", err
	}
	authorized := false
	for _, current := range listed.Tools {
		if current.Name != frozen.Name {
			continue
		}
		if !safeRunTool(current) {
			return "", fmt.Errorf("external write or unknown-risk tool requires explicit authorization")
		}
		definition, err := runToolDefinition(srv, current)
		if err != nil {
			return "", err
		}
		if definition.DefinitionHash != frozen.DefinitionHash {
			return "", fmt.Errorf("tool definition changed; a new Run is required")
		}
		authorized = true
		break
	}
	if !authorized {
		return "", fmt.Errorf("tool is no longer available")
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = frozen.Name
	request.Params.Arguments = args
	result, err := cli.CallTool(ctx, request)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	text := string(raw)
	if srv.AuthConfig != nil {
		for _, secret := range []string{srv.AuthConfig.APIKey, srv.AuthConfig.Token} {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[redacted]")
			}
		}
	}
	if result.IsError {
		return text, fmt.Errorf("MCP tool reported an error")
	}
	return text, nil
}
