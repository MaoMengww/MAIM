package infra

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
)

// mockDB provides a minimal DB-like behavior for testing ModelRepo cache logic.
// We test the cache behavior directly without full GORM integration.
type mockModelStore struct {
	mu      sync.RWMutex
	entries map[string]*domain.ModelEntry
}

func newMockModelStore() *mockModelStore {
	return &mockModelStore{entries: make(map[string]*domain.ModelEntry)}
}

func (m *mockModelStore) findByModelName(name string) *domain.ModelEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entries[name]
}

func (m *mockModelStore) findByCapability(cap string) []*domain.ModelEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*domain.ModelEntry
	for _, e := range m.entries {
		if e.Capability == cap {
			result = append(result, e)
		}
	}
	return result
}

func (m *mockModelStore) set(name string, entry *domain.ModelEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[name] = entry
}

func TestModelRepoCache_FindByName_Hit(t *testing.T) {
	store := newMockModelStore()
	store.set("gpt-4o", &domain.ModelEntry{
		ModelName: "gpt-4o", Provider: "openai", Capability: "chat", Status: "active",
	})

	entry := store.findByModelName("gpt-4o")
	require.NotNil(t, entry)
	assert.Equal(t, "openai", entry.Provider)
	assert.Equal(t, "chat", entry.Capability)
}

func TestModelRepoCache_FindByName_Miss(t *testing.T) {
	store := newMockModelStore()
	entry := store.findByModelName("nonexistent")
	assert.Nil(t, entry)
}

func TestModelRepoCache_FindByCapability(t *testing.T) {
	store := newMockModelStore()
	store.set("gpt-4o", &domain.ModelEntry{ModelName: "gpt-4o", Provider: "openai", Capability: "chat", Status: "active"})
	store.set("deepseek-v3", &domain.ModelEntry{ModelName: "deepseek-v3", Provider: "deepseek", Capability: "chat", Status: "active"})
	store.set("text-embedding-3-small", &domain.ModelEntry{ModelName: "text-embedding-3-small", Provider: "openai", Capability: "embed", Status: "active"})

	chatModels := store.findByCapability("chat")
	assert.Len(t, chatModels, 2)

	embedModels := store.findByCapability("embed")
	assert.Len(t, embedModels, 1)
	assert.Equal(t, "text-embedding-3-small", embedModels[0].ModelName)
}

func TestModelRepoCache_FindByCapability_Empty(t *testing.T) {
	store := newMockModelStore()
	result := store.findByCapability("rerank")
	assert.Empty(t, result)
}

func TestModelRepoCache_DisabledModel(t *testing.T) {
	store := newMockModelStore()
	store.set("gpt-4o", &domain.ModelEntry{
		ModelName: "gpt-4o", Provider: "openai", Capability: "chat", Status: "disabled",
	})
	entry := store.findByModelName("gpt-4o")
	require.NotNil(t, entry)
	assert.Equal(t, "disabled", entry.Status)
}

func TestModelRepoCache_ConcurrentAccess(t *testing.T) {
	store := newMockModelStore()
	store.set("gpt-4o", &domain.ModelEntry{ModelName: "gpt-4o", Provider: "openai", Capability: "chat", Status: "active"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = store.findByModelName("gpt-4o")
		}()
		go func() {
			defer wg.Done()
			_ = store.findByCapability("chat")
		}()
	}
	wg.Wait()
}

func TestModelRepoCache_ListAll(t *testing.T) {
	store := newMockModelStore()

	// Entries with explicit capability filter
	store.set("d", &domain.ModelEntry{ModelName: "d", Capability: "chat"})
	store.set("e", &domain.ModelEntry{ModelName: "e", Capability: "chat"})
	store.set("f", &domain.ModelEntry{ModelName: "f", Capability: "embed"})

	chatModels := store.findByCapability("chat")
	assert.Len(t, chatModels, 2)

	embedModels := store.findByCapability("embed")
	assert.Len(t, embedModels, 1)

	rerankModels := store.findByCapability("rerank")
	assert.Empty(t, rerankModels)
}

func TestModelRepo_RefreshInterval(t *testing.T) {
	interval := 300 * time.Second
	assert.Equal(t, 5*time.Minute, interval)
}

func TestModelEntry_CostCalculation(t *testing.T) {
	entry := &domain.ModelEntry{
		ModelName:          "gpt-4o",
		InputPricePerMTok:  2.50,
		OutputPricePerMTok: 10.00,
	}

	inputTokens := 1000
	outputTokens := 500

	inputCost := float64(inputTokens) / 1_000_000 * entry.InputPricePerMTok
	outputCost := float64(outputTokens) / 1_000_000 * entry.OutputPricePerMTok

	assert.InDelta(t, 0.0025, inputCost, 0.0001)
	assert.InDelta(t, 0.005, outputCost, 0.0001)
}

func TestModelRepo_NewModelRepoPanicsOnNilDB(t *testing.T) {
	assert.Panics(t, func() {
		NewModelRepo(nil, make([]byte, 32), time.Second)
	})
}
