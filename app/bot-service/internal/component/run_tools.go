package component

import (
	"bytes"
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
	"github.com/maomeng/aim/pkg/resourcegrant"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// RunTool is a credential-free, immutable definition. Authorization and secrets
// are resolved again at invocation; a snapshot is never a permanent permit.
type RunTool struct {
	ServerID        string          `json:"server_id"`
	Transport       string          `json:"transport"`
	URL             string          `json:"url"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Parameters      json.RawMessage `json:"parameters"`
	DefinitionHash  string          `json:"definition_hash"`
	Risk            string          `json:"risk"`
	TargetParameter string          `json:"target_parameter,omitempty"`
	CanShare        bool            `json:"can_share"`
	ConversationIDs []string        `json:"conversation_ids,omitempty"`
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
func TrustedRunToolPolicy(server model.McpServer, name string) (resourcegrant.ToolPolicy, bool) {
	var policies map[string]resourcegrant.ToolPolicy
	if server.AdvancedConfig != nil {
		policies = server.AdvancedConfig.ToolPolicies
	}
	return resourcegrant.TrustedToolPolicy(server.OwnerType, policies, name)
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
	return RunTool{ServerID: "builtin", Name: info.Name, Description: info.Desc, Parameters: parameters, DefinitionHash: hex.EncodeToString(digest[:]), Risk: "read", CanShare: true}, nil
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
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return RunTool{}, err
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return RunTool{}, err
		}
		redacted, err := json.Marshal(redactToolCredentials(value, server.AuthConfig))
		if err != nil {
			return RunTool{}, err
		}
		if string(canonical) != string(redacted) {
			return RunTool{}, fmt.Errorf("MCP tool definition contains credentials")
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
	policy, ok := TrustedRunToolPolicy(server, t.Name)
	if !ok {
		return RunTool{}, fmt.Errorf("tool has no trusted risk policy")
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return RunTool{}, err
	}
	hash := sha256.Sum256(append(append([]byte(t.Name+"\x00"+t.Description+"\x00"), policyJSON...), parameters...))
	return RunTool{ServerID: server.ID, Transport: server.Transport, URL: server.URL, Name: t.Name, Description: t.Description, Parameters: parameters, DefinitionHash: hex.EncodeToString(hash[:]), Risk: policy.Risk, TargetParameter: policy.TargetParameter, CanShare: policy.CanShare, ConversationIDs: policy.ConversationIDs}, nil
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
			if _, known := TrustedRunToolPolicy(srv, t.Name); !known {
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
func CallRunTool(ctx context.Context, srv model.McpServer, frozen RunTool, arguments string, beforeCall func(context.Context) error) (string, error) {
	if !srv.Enabled || srv.ID != frozen.ServerID || srv.Transport != frozen.Transport || srv.URL != frozen.URL {
		return "", fmt.Errorf("MCP access or routing changed")
	}
	var args map[string]any
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil || args == nil {
		return "", fmt.Errorf("tool arguments must be a JSON object")
	}
	cli, err := openRunMCP(ctx, srv)
	if err != nil {
		return "", err
	}
	// Transport cleanup must not replace the tool's result or trigger a retry.
	defer func() { _ = cli.Close() }()
	listed, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return "", err
	}
	authorized := false
	for _, current := range listed.Tools {
		if current.Name != frozen.Name {
			continue
		}
		policy, known := TrustedRunToolPolicy(srv, current.Name)
		if !known || policy.Risk != frozen.Risk || policy.TargetParameter != frozen.TargetParameter {
			return "", fmt.Errorf("trusted tool policy changed")
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
	if err := beforeCall(ctx); err != nil {
		return "", err
	}
	result, err := cli.CallTool(ctx, request)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	if srv.AuthConfig != nil {
		var value any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return "", err
		}
		raw, err = json.Marshal(redactToolCredentials(value, srv.AuthConfig))
		if err != nil {
			return "", err
		}
	}
	text := string(raw)
	if result.IsError {
		return text, fmt.Errorf("MCP tool reported an error")
	}
	return text, nil
}

func redactToolCredentials(value any, auth *model.MCPAuthConfig) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			redactedKey := key
			for _, secret := range []string{auth.APIKey, auth.Token} {
				if secret != "" {
					redactedKey = strings.ReplaceAll(redactedKey, secret, "[redacted]")
				}
			}
			if redactedKey != key {
				delete(current, key)
				// Drop secret-bearing properties without colliding with safe names.
				continue
			}
			current[key] = redactToolCredentials(item, auth)
		}
	case []any:
		for i, item := range current {
			current[i] = redactToolCredentials(item, auth)
		}
	case string:
		for _, secret := range []string{auth.APIKey, auth.Token} {
			if secret != "" {
				current = strings.ReplaceAll(current, secret, "[redacted]")
			}
		}
		var nested any
		dec := json.NewDecoder(strings.NewReader(current))
		dec.UseNumber()
		if dec.Decode(&nested) == nil {
			before, err := json.Marshal(nested)
			if err == nil {
				if raw, err := json.Marshal(redactToolCredentials(nested, auth)); err == nil && !bytes.Equal(before, raw) {
					return string(raw)
				}
			}
		}
		return current
	}
	return value
}
