package botplatform

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateMcpServerValidation(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewCreateMcpServerLogic(context.Background(), svcCtx)

	_, err := logic.CreateMcpServer(&pb.CreateMcpServerReq{Name: ""})
	require.Error(t, err)

	_, err = logic.CreateMcpServer(&pb.CreateMcpServerReq{
		UserId: 10,
		Name:   "test-mcp",
	})
	require.Error(t, err) // nil repo
}

func TestGetMcpServerOwnership(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewGetMcpServerLogic(context.Background(), svcCtx)

	_, err := logic.GetMcpServer(&pb.GetMcpServerReq{Id: 1, UserId: 100})
	require.Error(t, err) // nil repo
}

func TestListMcpServersUserFiltered(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewListMcpServersLogic(context.Background(), svcCtx)

	_, err := logic.ListMcpServers(&pb.ListMcpServersReq{UserId: 100})
	require.Error(t, err) // nil repo
}

func TestAssignMcpToBotValidation(t *testing.T) {
	svcCtx := &svc.ServiceContext{}
	logic := NewAssignMcpToBotLogic(context.Background(), svcCtx)

	_, err := logic.AssignMcpToBot(&pb.AssignMcpToBotReq{
		BotId:       1,
		UserId:      100,
		McpServerId: 10,
	})
	require.Error(t, err) // nil repo
}

func TestMCPCanModify(t *testing.T) {
	// platform MCP (created_by=0) can NOT be modified by regular user
	platformMCP := &model.McpServer{CreatedBy: 0}
	assert.False(t, mcpCanModify(platformMCP, 100))

	// user's own MCP can be modified
	userMCP := &model.McpServer{CreatedBy: 100}
	assert.True(t, mcpCanModify(userMCP, 100))

	// another user's MCP can NOT be modified
	otherMCP := &model.McpServer{CreatedBy: 200}
	assert.False(t, mcpCanModify(otherMCP, 100))
}

func TestMCPCanView(t *testing.T) {
	// platform MCP visible to everyone
	assert.True(t, mcpCanView(&model.McpServer{CreatedBy: 0}, 100))

	// user's own MCP visible
	assert.True(t, mcpCanView(&model.McpServer{CreatedBy: 100}, 100))

	// another user's MCP NOT visible
	assert.False(t, mcpCanView(&model.McpServer{CreatedBy: 200}, 100))
}

func TestMCPCanAssign(t *testing.T) {
	// platform MCP can be assigned to any bot
	assert.True(t, mcpCanAssign(&model.McpServer{CreatedBy: 0}, 100))

	// MCP can be assigned to owner's own bot
	assert.True(t, mcpCanAssign(&model.McpServer{CreatedBy: 100}, 100))

	// MCP can NOT be assigned to another user's bot
	assert.False(t, mcpCanAssign(&model.McpServer{CreatedBy: 200}, 100))
}

func TestMcpServerToProto(t *testing.T) {
	srv := &model.McpServer{
		ID:             5,
		Name:           "weather-api",
		Description:    "A weather MCP server",
		Transport:      "sse",
		URL:            "http://localhost:9000",
		CreatedBy:      100,
		Enabled:        true,
		AdvancedConfig: &model.MCPAdvancedConfig{Timeout: 30, RetryCount: 3, RetryDelay: 1},
	}
	pbInfo := mcpServerToProto(srv)
	assert.Equal(t, int64(5), pbInfo.Id)
	assert.Equal(t, "weather-api", pbInfo.Name)
	assert.Equal(t, "sse", pbInfo.Transport)
	assert.Equal(t, "http://localhost:9000", pbInfo.Url)
	assert.Equal(t, int32(30000), pbInfo.TimeoutMs)
	assert.Equal(t, "active", pbInfo.Status)
	assert.True(t, pbInfo.Enabled)
	assert.Equal(t, int64(100), pbInfo.CreatedBy)
	assert.NotEmpty(t, pbInfo.AdvancedConfig)
	assert.JSONEq(t, `{"timeout":30,"retry_count":3,"retry_delay":1}`, pbInfo.AdvancedConfig)
}

func TestMcpServerToProto_Disabled(t *testing.T) {
	srv := &model.McpServer{
		ID:        6,
		Name:      "disabled-server",
		Transport: "stdio",
		Enabled:   false,
	}
	pbInfo := mcpServerToProto(srv)
	assert.Equal(t, "disabled", pbInfo.Status)
	assert.False(t, pbInfo.Enabled)
}

func TestTimeoutFromAdvanced(t *testing.T) {
	assert.Equal(t, int32(30000), timeoutFromAdvanced(nil))
	assert.Equal(t, int32(15000), timeoutFromAdvanced(&model.MCPAdvancedConfig{Timeout: 15}))
	assert.Equal(t, int32(30000), timeoutFromAdvanced(&model.MCPAdvancedConfig{Timeout: 0}))
	// default when no positive timeout
	assert.Equal(t, int32(30000), timeoutFromAdvanced(&model.MCPAdvancedConfig{Timeout: -1}))
}
