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
	"github.com/maomeng/aim/pkg/logx"
)

// --- mocks ---

type mockModelRepo struct {
	mu     sync.RWMutex
	byName map[string]*domain.ModelEntry
	byID   map[int64]*domain.ModelEntry
	nextID int64
}

func newMockModelRepo() *mockModelRepo {
	return &mockModelRepo{
		byName: make(map[string]*domain.ModelEntry),
		byID:   make(map[int64]*domain.ModelEntry),
	}
}

func (m *mockModelRepo) FindByID(id int64) (*domain.ModelEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byID[id], nil
}

func (m *mockModelRepo) FindByName(name string) (*domain.ModelEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byName[name], nil
}

func (m *mockModelRepo) FindByCapability(_ string) ([]*domain.ModelEntry, error) {
	return nil, nil
}

func (m *mockModelRepo) ListAll() []*domain.ModelEntry { return nil }
func (m *mockModelRepo) Refresh() error                { return nil }
func (m *mockModelRepo) Close()                        {}

func (m *mockModelRepo) add(name, provider, capability, status, apiKey, baseURL string, inputPrice, outputPrice float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	entry := &domain.ModelEntry{
		ID: id, ModelName: name, Provider: provider, Capability: capability, Status: status,
		APIKey: apiKey, BaseURL: baseURL, InputPricePerMTok: inputPrice, OutputPricePerMTok: outputPrice,
	}
	m.byID[id] = entry
	m.byName[name] = entry
}

type mockRateLimiter struct {
	allowErr error
}

func (r *mockRateLimiter) Allow(_ context.Context, _ string) error           { return r.allowErr }
func (r *mockRateLimiter) IncrConcurrency(_ context.Context, _ string) error { return nil }
func (r *mockRateLimiter) DecrConcurrency(_ context.Context, _ string) error { return nil }

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
		RateLimiter: &mockRateLimiter{},
		Logger:      logx.DefaultLogger(),
	}
}

// --- tests ---

func TestChat_ModelNotFound(t *testing.T) {
	h := NewLLMGatewayHandler(newMockServiceContext())
	_, err := h.Chat(context.Background(), &pb.ChatReq{ModelId: 999})
	require.Error(t, err)
}

func TestChat_ModelDisabled(t *testing.T) {
	svcCtx := newMockServiceContext()
	svcCtx.ModelRepo.(*mockModelRepo).add("gpt-4o", "openai", "chat", "disabled", "sk-test", "https://api.openai.com", 2.5, 10)
	h := NewLLMGatewayHandler(svcCtx)
	_, err := h.Chat(context.Background(), &pb.ChatReq{ModelId: 0})
	require.Error(t, err)
}

func TestChat_RateLimited(t *testing.T) {
	svcCtx := newMockServiceContext()
	svcCtx.ModelRepo.(*mockModelRepo).add("gpt-4o", "openai", "chat", "active", "sk-test", "https://api.openai.com", 2.5, 10)
	svcCtx.RateLimiter = &mockRateLimiter{allowErr: errors.New(errors.CodeTooManyRequests, "rate limited")}
	h := NewLLMGatewayHandler(svcCtx)
	_, err := h.Chat(context.Background(), &pb.ChatReq{ModelId: 0})
	require.Error(t, err)
}
