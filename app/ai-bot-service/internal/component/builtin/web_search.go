package builtin

import (
	"context"
	"time"

	"github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
	"github.com/cloudwego/eino/components/tool"
)

func newWebSearch() (tool.BaseTool, error) {
	return duckduckgo.NewTextSearchTool(context.Background(), &duckduckgo.Config{
		ToolName:   "web_search",
		ToolDesc:   "Search the web using DuckDuckGo. Returns relevant results with titles, snippets, and URLs. Use this when the user asks about current events, facts, or information that may not be in the bot's training data.",
		MaxResults: 8,
		Timeout:    30 * time.Second,
	})
}
