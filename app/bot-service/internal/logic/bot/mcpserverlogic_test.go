package bot

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func (m *enhancedMockRepo) CreateMcpServer(ctx context.Context, server *model.McpServer) error {
	m.servers[server.ID] = server
	return nil
}
func (m *enhancedMockRepo) GetMcpServer(ctx context.Context, serverID string) (*model.McpServer, error) {
	server, ok := m.servers[serverID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return server, nil
}

func TestCreateMcpServerValidation(t *testing.T) {
	r := newEnhancedMockRepo()
	logic := NewCreateMcpServerLogic(testCaller(t), &svc.ServiceContext{Repo: r})
	for _, request := range []*pb.CreateMcpServerReq{
		{UserId: testOwnerID, OwnerType: "user", OwnerId: stringPtr(testOwnerID)},
		{UserId: testOwnerID, OwnerType: "platform", Name: "elevated"},
		{UserId: testOwnerID, OwnerType: "user", Name: "missing owner"},
		{UserId: testOwnerID, OwnerType: "user", OwnerId: stringPtr(testOtherOwnerID), Name: "other owner"},
		{UserId: testOwnerID, OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "bad config", AuthConfig: "invalid"},
	} {
		_, err := logic.CreateMcpServer(request)
		require.Error(t, err)
	}
	created, err := logic.CreateMcpServer(&pb.CreateMcpServerReq{UserId: testOwnerID, OwnerType: "user", OwnerId: stringPtr(testOwnerID), Name: "owned"})
	require.NoError(t, err)
	require.NoError(t, identity.Validate(created.Id))
	assert.Equal(t, "user", created.OwnerType)
	assert.Equal(t, testOwnerID, created.GetOwnerId())
}

func TestGetMcpServerOwnership(t *testing.T) {
	r := newEnhancedMockRepo()
	// Creation audit is not a grant of ownership.
	r.servers[testServerID] = &model.McpServer{ID: testServerID, OwnerType: "user", OwnerID: stringPtr(testOtherOwnerID), CreatedBy: stringPtr(testOwnerID)}
	logic := NewGetMcpServerLogic(testCaller(t), &svc.ServiceContext{Repo: r})
	_, err := logic.GetMcpServer(&pb.GetMcpServerReq{Id: testServerID, UserId: testOwnerID})
	assert.ErrorIs(t, err, ErrBotForbidden)
	r.servers[testServerID].OwnerType = "platform"
	r.servers[testServerID].OwnerID = nil
	visible, err := logic.GetMcpServer(&pb.GetMcpServerReq{Id: testServerID, UserId: testOwnerID})
	require.NoError(t, err)
	assert.Equal(t, testServerID, visible.Id)
	assert.Nil(t, visible.OwnerId)
	assert.Equal(t, testOwnerID, visible.GetCreatedBy())
}

func TestMCPCanModify(t *testing.T) {
	assert.False(t, mcpCanModify(&model.McpServer{OwnerType: "platform", CreatedBy: stringPtr(testOwnerID)}, testOwnerID))
	assert.True(t, mcpCanModify(&model.McpServer{OwnerType: "user", OwnerID: stringPtr(testOwnerID)}, testOwnerID))
	assert.False(t, mcpCanModify(&model.McpServer{OwnerType: "user", OwnerID: stringPtr(testOtherOwnerID), CreatedBy: stringPtr(testOwnerID)}, testOwnerID))
}
func TestMCPCanView(t *testing.T) {
	assert.True(t, mcpCanView(&model.McpServer{OwnerType: "platform"}, testOwnerID))
	assert.True(t, mcpCanView(&model.McpServer{OwnerType: "user", OwnerID: stringPtr(testOwnerID)}, testOwnerID))
	assert.False(t, mcpCanView(&model.McpServer{OwnerType: "user", OwnerID: stringPtr(testOtherOwnerID)}, testOwnerID))
}
func TestMCPCanAssign(t *testing.T) {
	assert.True(t, mcpCanAssign(&model.McpServer{OwnerType: "platform"}, testOwnerID))
	assert.True(t, mcpCanAssign(&model.McpServer{OwnerType: "user", OwnerID: stringPtr(testOwnerID)}, testOwnerID))
	assert.False(t, mcpCanAssign(&model.McpServer{OwnerType: "user", OwnerID: stringPtr(testOtherOwnerID)}, testOwnerID))
}
