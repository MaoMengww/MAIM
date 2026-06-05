package snowflake

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNodeValid(t *testing.T) {
	tests := []int64{0, 1, 10, 100, 500, 1023}
	for _, wid := range tests {
		t.Run(fmt.Sprintf("worker_%d", wid), func(t *testing.T) {
			n, err := NewNode(wid)
			require.NoError(t, err)
			assert.NotNil(t, n)
			assert.Equal(t, wid, n.workerID)
		})
	}
}

func TestNewNodeInvalid(t *testing.T) {
	tests := []int64{-1, -100, 1024, 9999}
	for _, wid := range tests {
		t.Run(fmt.Sprintf("worker_%d", wid), func(t *testing.T) {
			n, err := NewNode(wid)
			assert.Error(t, err)
			assert.Nil(t, n)
		})
	}
}

func TestGenerate(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	id := n.Generate()
	assert.Greater(t, id, int64(0))

	extractedWorker := ExtractWorkerID(id)
	assert.Equal(t, int64(1), extractedWorker)

	ts := ExtractTimestamp(id)
	assert.True(t, time.Since(ts) < time.Minute)
	assert.True(t, ts.After(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)))
}

func TestGenerateUniqueness(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	count := 10000
	ids := make(map[int64]bool, count)
	for i := 0; i < count; i++ {
		id := n.Generate()
		assert.False(t, ids[id], "duplicate ID: %d", id)
		ids[id] = true
	}
	assert.Equal(t, count, len(ids))
}

func TestGenerateString(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	s := n.GenerateString()
	assert.NotEmpty(t, s)
	assert.Greater(t, len(s), 10)

	for _, c := range s {
		assert.True(t, c >= '0' && c <= '9')
	}
}

func TestGenerateWithError(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	id, err := n.GenerateWithError()
	assert.NoError(t, err)
	assert.Greater(t, id, int64(0))

	var nilNode *Node
	_, err = nilNode.GenerateWithError()
	assert.Error(t, err)
}

func TestExtractComponents(t *testing.T) {
	n, err := NewNode(42)
	require.NoError(t, err)

	id := n.Generate()

	workerID := ExtractWorkerID(id)
	assert.Equal(t, int64(42), workerID)

	seq := ExtractSequence(id)
	assert.GreaterOrEqual(t, seq, int64(0))
	assert.LessOrEqual(t, seq, int64(4095))

	ts := ExtractTimestamp(id)
	assert.WithinDuration(t, time.Now(), ts, 5*time.Second)
}

func TestConcurrentGeneration(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	var wg sync.WaitGroup
	count := 1000
	workers := 10
	ids := make(chan int64, count*workers)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < count; i++ {
				ids <- n.Generate()
			}
		}()
	}

	wg.Wait()
	close(ids)

	seen := make(map[int64]bool)
	for id := range ids {
		assert.False(t, seen[id], "duplicate ID in concurrent generation: %d", id)
		seen[id] = true
	}
	assert.Equal(t, count*workers, len(seen))
}

func TestMultipleWorkersUniqueness(t *testing.T) {
	count := 1000
	seen := make(map[int64]bool)

	for wid := 0; wid < 10; wid++ {
		n, err := NewNode(int64(wid))
		require.NoError(t, err)
		for i := 0; i < count; i++ {
			id := n.Generate()
			assert.False(t, seen[id], "duplicate across workers: %d", id)
			seen[id] = true
		}
	}
	assert.Equal(t, count*10, len(seen))
}

func TestMonotonicIncreasing(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	prev := int64(0)
	for i := 0; i < 5000; i++ {
		id := n.Generate()
		assert.Greater(t, id, prev)
		prev = id
	}
}

func TestSequenceResetOnNewMs(t *testing.T) {
	n, err := NewNode(1)
	require.NoError(t, err)

	id1 := n.Generate()
	seq1 := ExtractSequence(id1)

	time.Sleep(2 * time.Millisecond)

	id2 := n.Generate()
	seq2 := ExtractSequence(id2)

	assert.Equal(t, int64(0), seq2)
	_ = seq1
}
