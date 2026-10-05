package consumer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDedup_NilClient(t *testing.T) {
	d := NewDedup(nil)
	isDup, err := d.IsDuplicate(context.Background(), "event-001")
	assert.NoError(t, err)
	assert.False(t, isDup, "should skip dedup when redis is nil")
}

func TestNewDedup(t *testing.T) {
	d := NewDedup(nil)
	assert.NotNil(t, d)

	d2 := NewDedup(nil)
	assert.NotNil(t, d2)
}
