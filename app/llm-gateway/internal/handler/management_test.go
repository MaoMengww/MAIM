package handler

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type managementListRegistry struct {
	*mockModelRepo
	entries []*domain.ModelEntry
}

func (r *managementListRegistry) ListAll() []*domain.ModelEntry { return r.entries }

func TestManagementListModelsNeverDisclosesCredentials(t *testing.T) {
	owner, err := identity.New()
	require.NoError(t, err)
	modelID, err := identity.New()
	require.NoError(t, err)
	connectionID, err := identity.New()
	require.NoError(t, err)
	poolID, err := identity.New()
	require.NoError(t, err)
	ctx := context.WithValue(t.Context(), interceptor.ContextKeyUserID, owner)
	s := newMockServiceContext()
	s.ModelRepo = &managementListRegistry{mockModelRepo: newMockModelRepo(), entries: []*domain.ModelEntry{{
		ID: modelID, ModelName: "private-model", Provider: "openai", Capability: "chat",
		ConnectionID: connectionID, QuotaPoolID: poolID,
		Status: "active", OwnerType: "user", OwnerID: &owner, APIKey: "credential-must-stay-server-side",
	}}}
	response, err := NewLLMGatewayHandler(s).ListModels(ctx, &pb.ListModelsReq{OwnerId: &owner})
	require.NoError(t, err)
	require.Len(t, response.Items, 1)
	wire, err := protocol.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "credential-must-stay-server-side")
}
