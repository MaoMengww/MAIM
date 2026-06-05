package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestComputeScore_FullWeight(t *testing.T) {
	now := time.Now()
	lastAccess := now.Add(-5 * time.Hour) // recently accessed
	maxAccess := 10

	m := &MemoryItem{
		MemoryType:     "fact",
		Importance:     0.8,
		AccessCount:    10,
		LastAccessedAt: &lastAccess,
	}

	score := ComputeScore(m, now, maxAccess)

	// importance=0.8*0.5 + recency≈0.99*0.3 + frequency=1.0*0.2 + typeBonus=1.0
	// = 0.4 + 0.297 + 0.2 + 1.0 ≈ 1.897
	assert.Greater(t, score, 1.5)
	assert.Less(t, score, 2.0)
}

func TestComputeScore_NeverAccessed(t *testing.T) {
	now := time.Now()
	maxAccess := 10

	m := &MemoryItem{
		MemoryType:     "fact",
		Importance:     0.5,
		AccessCount:    0,
		LastAccessedAt: nil,
	}

	score := ComputeScore(m, now, maxAccess)

	// importance=0.5*0.5 + recency=0.0 + frequency=0.0 + typeBonus=1.0 = 1.25
	assert.InDelta(t, 1.25, score, 0.01)
}

func TestComputeScore_OldAccess(t *testing.T) {
	now := time.Now()
	oldAccess := now.Add(-90 * 24 * time.Hour) // 90 days ago
	maxAccess := 20

	m := &MemoryItem{
		MemoryType:     "fact",
		Importance:     0.8,
		AccessCount:    5,
		LastAccessedAt: &oldAccess,
	}

	score := ComputeScore(m, now, maxAccess)

	// recency = exp(-90/30) = exp(-3) ≈ 0.05
	// importance=0.8*0.5=0.4, recency≈0.05*0.3=0.015, frequency=5/20*0.2=0.05, typeBonus=1.0
	// total ≈ 1.465
	assert.Greater(t, score, 1.0)
	assert.Less(t, score, 1.6)
}

func TestComputeScore_LowImportance(t *testing.T) {
	now := time.Now()
	lastAccess := now.Add(-1 * time.Hour)
	maxAccess := 5

	m := &MemoryItem{
		MemoryType:     "episode",
		Importance:     0.1,
		AccessCount:    1,
		LastAccessedAt: &lastAccess,
	}

	score := ComputeScore(m, now, maxAccess)
	// Should be > 0
	assert.Greater(t, score, 0.3)
	assert.Less(t, score, 1.0)
}

func TestComputeScore_ImportanceClamped(t *testing.T) {
	now := time.Now()
	lastAccess := now
	maxAccess := 1

	m := &MemoryItem{
		MemoryType:     "fact",
		Importance:     1.5, // > 1.0, should be clamped
		AccessCount:    1,
		LastAccessedAt: &lastAccess,
	}

	score := ComputeScore(m, now, maxAccess)
	// importance clamped to 1.0, recency=exp(0)=1, frequency=1.0
	// 1.0*0.5 + 1.0*0.3 + 1.0*0.2 + 1.0 = 2.0
	assert.Greater(t, score, 1.5)
	assert.LessOrEqual(t, score, 2.0)
}

func TestComputeScore_ZeroMaxAccess(t *testing.T) {
	now := time.Now()

	m := &MemoryItem{
		MemoryType:     "fact",
		Importance:     0.5,
		AccessCount:    0,
		LastAccessedAt: nil,
	}

	score := ComputeScore(m, now, 0)
	// frequency = 0/0 → 0, importance*0.5 + 0 + 0 + typeBonus=1.0 = 1.25
	assert.InDelta(t, 1.25, score, 0.01)
}
