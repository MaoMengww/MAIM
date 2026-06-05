package infra

import (
	"context"
	"sync"
	"time"

	"github.com/maomeng/aim/app/llm-gateway/internal/domain"
	"github.com/maomeng/aim/app/llm-gateway/internal/model"
	"github.com/maomeng/aim/pkg/crypto"
	"github.com/maomeng/aim/pkg/database"
)

type ModelRepo struct {
	db           *database.DB
	encKey       []byte
	mu           sync.RWMutex
	cache        map[string]*domain.ModelEntry
	byID         map[int64]*domain.ModelEntry
	byCapability map[string][]*domain.ModelEntry
	interval     time.Duration
	stopCh       chan struct{}
}

func NewModelRepo(db *database.DB, encKey []byte, interval time.Duration) *ModelRepo {
	r := &ModelRepo{
		db:           db,
		encKey:       encKey,
		cache:        make(map[string]*domain.ModelEntry),
		byID:         make(map[int64]*domain.ModelEntry),
		byCapability: make(map[string][]*domain.ModelEntry),
		interval:     interval,
		stopCh:       make(chan struct{}),
	}
	if err := r.Refresh(); err != nil {
		panic("model repo initial load failed: " + err.Error())
	}
	go r.refreshLoop()
	return r
}

func (r *ModelRepo) refreshLoop() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.Refresh(); err != nil {
				// log error but keep serving from cache
				_ = err
			}
		case <-r.stopCh:
			return
		}
	}
}

func (r *ModelRepo) Refresh() error {
	var records []model.ModelRegistry
	if err := r.db.WithContext(context.Background()).Where("status = ?", "active").Find(&records).Error; err != nil {
		return err
	}

	newCache := make(map[string]*domain.ModelEntry, len(records))
	newByID := make(map[int64]*domain.ModelEntry, len(records))
	newByCap := make(map[string][]*domain.ModelEntry)

	for _, rec := range records {
		var apiKey string
		if rec.APIKeyEncrypted != "" {
			var err error
			apiKey, err = crypto.DecryptString(rec.APIKeyEncrypted, r.encKey)
			if err != nil {
				apiKey = ""
			}
		}
		entry := &domain.ModelEntry{
			ID:                 rec.ID,
			ModelName:          rec.ModelName,
			Provider:           rec.Provider,
			Capability:         rec.Capability,
			BaseURL:            rec.BaseURL,
			APIKeyEncrypted:    rec.APIKeyEncrypted,
			APIKey:             apiKey,
			ContextWindow:      rec.ContextWindow,
			MaxOutputTokens:    rec.MaxOutputTokens,
			InputPricePerMTok:  rec.InputPricePerMTok,
			OutputPricePerMTok: rec.OutputPricePerMTok,
			OwnerID:            rec.OwnerID,
			Status:             rec.Status,
			Metadata:           rec.Metadata,
		}
		newCache[rec.ModelName] = entry
		newByID[rec.ID] = entry
		newByCap[rec.Capability] = append(newByCap[rec.Capability], entry)
	}

	r.mu.Lock()
	r.cache = newCache
	r.byID = newByID
	r.byCapability = newByCap
	r.mu.Unlock()
	return nil
}

func (r *ModelRepo) FindByID(modelID int64) (*domain.ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.byID[modelID]
	if !ok {
		return nil, nil
	}
	return entry, nil
}

func (r *ModelRepo) FindByName(modelName string) (*domain.ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.cache[modelName]
	if !ok {
		return nil, nil
	}
	return entry, nil
}

func (r *ModelRepo) FindByCapability(capability string) ([]*domain.ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byCapability[capability], nil
}

func (r *ModelRepo) ListAll() []*domain.ModelEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*domain.ModelEntry, 0, len(r.byID))
	for _, v := range r.byID {
		result = append(result, v)
	}
	return result
}

func (r *ModelRepo) Close() {
	close(r.stopCh)
}
