package memory

import (
	"context"
	"time"

	"github.com/maomeng/aim/pkg/logx"
)

// Manager is the memory system facade.
type Manager struct {
	logger    logx.Logger
	store     Store
	extractor *Extractor
	evictor   *Evictor
}

// NewManager creates a new memory Manager.
func NewManager(logger logx.Logger, store Store, extractor *Extractor) *Manager {
	return &Manager{
		logger:    logger,
		store:     store,
		extractor: extractor,
		evictor:   NewEvictor(store),
	}
}

// Retrieve returns the top memories for a user-bot pair.
func (m *Manager) Retrieve(extractCtx context.Context, botID, userID int64, query string, limit int) ([]*MemoryItem, error) {
	if limit <= 0 {
		limit = 5
	}
	if query != "" {
		return m.store.SearchByUser(extractCtx, botID, userID, query, limit)
	}
	return m.store.FindByUser(extractCtx, botID, userID, limit)
}

// CreateAsync extracts memories from dialog and stores them asynchronously.
func (m *Manager) CreateAsync(extractCtx context.Context, botID, userID int64, dialog []Message) {
	go func() {
		// Detach from parent cancellation but keep trace/metadata context
		extractCtx := context.WithoutCancel(extractCtx)
		extractCtx, cancel := context.WithTimeout(extractCtx, 120*time.Second)
		defer cancel()

		result, err := m.extractor.Extract(extractCtx, dialog)
		if err != nil {
			m.logger.WithContext(extractCtx).Errorf("memory extraction failed: bot_id=%d user_id=%d error=%v", botID, userID, err)
			return
		}

		if len(result.Facts) == 0 {
			m.logger.WithContext(extractCtx).Infof("memory extraction returned no facts: bot_id=%d user_id=%d", botID, userID)
			return
		}

		for _, fact := range result.Facts {
			item := &MemoryItem{
				BotID:      botID,
				UserID:     userID,
				MemoryType: "fact",
				Content:    fact.Content,
				Category:   fact.Category,
				Importance: fact.Importance,
				Confidence: fact.Confidence,
				CreatedAt:  time.Now(),
			}
			if err := m.store.Insert(extractCtx, item); err != nil {
				m.logger.WithContext(extractCtx).Errorf("memory insert failed: bot_id=%d user_id=%d content=%q error=%v", botID, userID, fact.Content, err)
			}
			if err := m.evictor.EvictIfNeeded(extractCtx, botID, userID); err != nil {
				m.logger.WithContext(extractCtx).Errorf("memory evict failed: bot_id=%d user_id=%d error=%v", botID, userID, err)
			}
		}
	}()
}

// ForgetByUser deletes a single memory for a user.
func (m *Manager) ForgetByUser(extractCtx context.Context, botID, userID, memoryID int64) error {
	item, err := m.store.FindByID(extractCtx, memoryID)
	if err != nil {
		return err
	}
	if item.BotID != botID || item.UserID != userID {
		return errOwnershipMismatch
	}
	return m.store.Delete(extractCtx, memoryID)
}

// ClearByUser clears all memories for a user under a bot.
func (m *Manager) ClearByUser(extractCtx context.Context, botID, userID int64) error {
	return m.store.DeleteByUser(extractCtx, botID, userID)
}

var errOwnershipMismatch = &ownershipError{}

type ownershipError struct{}

func (*ownershipError) Error() string { return "memory ownership mismatch" }
