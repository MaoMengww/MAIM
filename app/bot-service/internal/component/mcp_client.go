package component

import (
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/maomeng/aim/app/bot-service/internal/model"
)

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
