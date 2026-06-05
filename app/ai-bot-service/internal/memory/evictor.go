package memory

import (
	"context"
	"math"
	"sort"
	"time"
)

const maxMemoriesPerUser = 200

// Evictor manages memory capacity and eviction.
type Evictor struct {
	store Store
}

// NewEvictor creates a new eviction manager.
func NewEvictor(store Store) *Evictor {
	return &Evictor{store: store}
}

// EvictIfNeeded checks and performs eviction if the user exceeds the memory cap.
func (e *Evictor) EvictIfNeeded(ctx context.Context, botID, userID int64) error {
	count, err := e.store.CountByUser(ctx, botID, userID)
	if err != nil {
		return err
	}
	if count <= maxMemoriesPerUser {
		return nil
	}

	// Fetch all memories for this user
	mems, err := e.store.FindByUser(ctx, botID, userID, int(count))
	if err != nil {
		return err
	}

	// Compute final_score for each
	type scored struct {
		id    int64
		score float64
	}

	maxAccess := 1
	for _, m := range mems {
		if m.AccessCount > maxAccess {
			maxAccess = m.AccessCount
		}
	}

	now := time.Now()
	var scoredMems []scored
	for _, m := range mems {
		score := ComputeScore(m, now, maxAccess)
		scoredMems = append(scoredMems, scored{id: m.ID, score: score})
	}

	// Sort by score ascending
	sort.Slice(scoredMems, func(i, j int) bool {
		return scoredMems[i].score < scoredMems[j].score
	})

	// Only delete the excess number of memories
	excess := int(count - maxMemoriesPerUser)
	if excess > len(scoredMems) {
		excess = len(scoredMems)
	}

	ids := make([]int64, 0, excess)
	for i := 0; i < excess; i++ {
		ids = append(ids, scoredMems[i].id)
	}

	// Batch delete
	for _, id := range ids {
		_ = e.store.Delete(ctx, id)
	}

	return nil
}

// ComputeScore calculates the unified final score for memory ranking.
//
//	final_score = importance × 0.5 + recency × 0.3 + frequency × 0.2 + typeBonus
func ComputeScore(m *MemoryItem, now time.Time, maxAccess int) float64 {
	importance := m.Importance
	if importance > 1.0 {
		importance = 1.0
	}

	// recency: exp(-days_since_last_access / 30)
	recency := 0.0
	if m.LastAccessedAt != nil {
		days := now.Sub(*m.LastAccessedAt).Hours() / 24
		recency = math.Exp(-days / 30)
	}

	// frequency: access_count / max_access_count
	frequency := 0.0
	if maxAccess > 0 {
		frequency = float64(m.AccessCount) / float64(maxAccess)
	}

	// typeBonus: fact type has higher retention weight
	typeBonus := 0.0
	if m.MemoryType == "fact" {
		typeBonus = 1.0
	} else {
		typeBonus = 0.3
	}

	return importance*0.5 + recency*0.3 + frequency*0.2 + typeBonus
}
