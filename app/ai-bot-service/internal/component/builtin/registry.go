package builtin

import "github.com/cloudwego/eino/components/tool"

// NewFunc creates a built-in tool instance.
type NewFunc func() (tool.BaseTool, error)

// Registry maps tool names to constructors.
var Registry = map[string]NewFunc{
	"web_search": newWebSearch,
}
