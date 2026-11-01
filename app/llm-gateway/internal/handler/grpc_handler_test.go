package handler

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maomeng/aim/app/llm-gateway/internal/config"
	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/app/llm-gateway/internal/svc"
	pb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/logx"
)

// --- mocks ---

type mockModelRepo struct {
	mu     sync.RWMutex
	byName map[string]*domain.ModelEntry
	byID   map[string]*domain.ModelEntry
}

func newMockModelRepo() *mockModelRepo {
	return &mockModelRepo{
		byName: make(map[string]*domain.ModelEntry),
		byID:   make(map[string]*domain.ModelEntry),
	}
}

func (m *mockModelRepo) FindByID(ctx context.Context, id string) (*domain.ModelEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id], nil
}

func (m *mockModelRepo) FindByName(name string) (*domain.ModelEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byName[name], nil
}

func (m *mockModelRepo) FindByCapability(_ string, _ *string) ([]*domain.ModelEntry, error) {
	return nil, nil
}

func (m *mockModelRepo) ListAll() []*domain.ModelEntry { return nil }
func (m *mockModelRepo) Refresh() error                { return nil }
func (m *mockModelRepo) Close()                        {}

func (m *mockModelRepo) add(t *testing.T, name, provider, capability, status, apiKey, baseURL string, inputPrice, outputPrice float64) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := identity.New()
	require.NoError(t, err)
	entry := &domain.ModelEntry{
		ID: id, ModelName: name, Provider: provider, Capability: capability, Status: status,
		APIKey: apiKey, BaseURL: baseURL, InputPricePerMTok: inputPrice, OutputPricePerMTok: outputPrice,
		OwnerType: "platform",
	}
	m.byID[id] = entry
	m.byName[name] = entry
	return id
}

type mockCapacity struct{}

func (*mockCapacity) Acquire(ctx context.Context, _ domain.AdmissionRequest) (domain.CapacityPermit, error) {
	return &mockPermit{ctx: ctx}, ctx.Err()
}
func (*mockCapacity) Close() error { return nil }

type mockPermit struct{ ctx context.Context }

func (p *mockPermit) Context() context.Context { return p.ctx }
func (*mockPermit) Release()                   {}
func (*mockPermit) Abort()                     {}

type mockBillingRepo struct {
	mu      sync.Mutex
	records []*model.BillingRecord
}

func (r *mockBillingRepo) Record(rec *model.BillingRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	return nil
}

func newMockServiceContext() *svc.ServiceContext {
	return &svc.ServiceContext{
		Config:      config.Config{},
		EncKey:      make([]byte, 32),
		ModelRepo:   newMockModelRepo(),
		BillingRepo: &mockBillingRepo{},
		Capacity:    &mockCapacity{},
		Logger:      logx.DefaultLogger(),
	}
}

// --- tests ---

func TestChat_ModelNotFound(t *testing.T) {
	h := NewLLMGatewayHandler(newMockServiceContext())
	id, err := identity.New()
	require.NoError(t, err)
	_, err = h.Chat(t.Context(), &pb.ChatReq{ModelId: id})
	assertErrorCode(t, err, errors.CodeNotFound)
}

func TestChat_ModelDisabled(t *testing.T) {
	svcCtx := newMockServiceContext()
	id := svcCtx.ModelRepo.(*mockModelRepo).add(t, "gpt-4o", "openai", "chat", "disabled", "sk-test", "https://api.openai.com", 2.5, 10)
	h := NewLLMGatewayHandler(svcCtx)
	_, err := h.Chat(t.Context(), &pb.ChatReq{ModelId: id})
	assertErrorCode(t, err, errors.CodeForbidden)
}

func assertErrorCode(t *testing.T, err error, code int) {
	t.Helper()
	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok, "expected business error, got %v", err)
	require.Equal(t, code, bizErr.Code)
}
