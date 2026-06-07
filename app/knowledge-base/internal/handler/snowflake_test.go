package handler

import (
	"testing"

	"github.com/maomeng/aim/pkg/snowflake"
)

func TestSnowflakeGeneratesNonZeroID(t *testing.T) {
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("create snowflake node failed: %v", err)
	}
	id, err := node.Generate()
	if err != nil {
		t.Fatalf("generate id failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero snowflake id")
	}
}

func TestSnowflakeIDsAreUnique(t *testing.T) {
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("create snowflake node failed: %v", err)
	}
	seen := make(map[int64]bool)
	for i := 0; i < 100; i++ {
		id, err := node.Generate()
		if err != nil {
			t.Fatalf("generate id failed: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate snowflake id: %d", id)
		}
		seen[id] = true
	}
}

func TestSnowflakeIDsIncrease(t *testing.T) {
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("create snowflake node failed: %v", err)
	}
	prev, err := node.Generate()
	if err != nil {
		t.Fatalf("generate id failed: %v", err)
	}
	for i := 0; i < 10; i++ {
		curr, err := node.Generate()
		if err != nil {
			t.Fatalf("generate id failed: %v", err)
		}
		if curr <= prev {
			t.Fatalf("snowflake id %d <= previous %d", curr, prev)
		}
		prev = curr
	}
}
