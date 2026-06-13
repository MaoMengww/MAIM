package snowflake

import (
	"fmt"
	"time"

	bwsnowflake "github.com/bwmarrin/snowflake"

	"github.com/maomeng/aim/pkg/errors"
)

// Bit layout values mirroring bwmarrin/snowflake exported vars.
// These are vars (not consts) because bwmarrin's Epoch/NodeBits/StepBits are vars.
var (
	epoch     = bwsnowflake.Epoch            // 1288834974657
	nodeBits  = bwsnowflake.NodeBits         // 10
	stepBits  = bwsnowflake.StepBits         // 12
	timeShift = nodeBits + stepBits          // 22
	nodeShift = stepBits                     // 12
	stepMask  = int64(-1 ^ (-1 << stepBits)) // 4095
	nodeMask  = int64(-1 ^ (-1 << nodeBits)) // 1023
)

// Node wraps a bwmarrin/snowflake generator, preserving the legacy public API.
type Node struct {
	inner *bwsnowflake.Node
}

// NewNode creates a snowflake generator for the given worker ID (0–1023).
func NewNode(workerID int64) (*Node, error) {
	inner, err := bwsnowflake.NewNode(workerID)
	if err != nil {
		return nil, err
	}
	return &Node{inner: inner}, nil
}

// Generate returns a new unique snowflake ID. The error return is always nil
// when the node is non-nil; clock-rollback is handled internally by bwmarrin.
func (n *Node) Generate() (int64, error) {
	if n == nil || n.inner == nil {
		return 0, fmt.Errorf("snowflake node is nil")
	}
	return n.inner.Generate().Int64(), nil
}

// GenerateString returns the ID as a decimal string.
func (n *Node) GenerateString() (string, error) {
	if n == nil || n.inner == nil {
		return "", fmt.Errorf("snowflake node is nil")
	}
	return n.inner.Generate().String(), nil
}

// GenerateWithError returns a new ID, or a business error (code 1006) when the
// node is nil. It exists for callers that need structured error codes.
func (n *Node) GenerateWithError() (int64, error) {
	if n == nil || n.inner == nil {
		return 0, errors.New(errors.CodeInternal, "snowflake node is nil")
	}
	return n.Generate()
}

// ExtractTimestamp decodes the creation time embedded in the ID.
func ExtractTimestamp(id int64) time.Time {
	ts := (id >> timeShift) + epoch
	return time.UnixMilli(ts)
}

// ExtractWorkerID decodes the worker (machine) ID embedded in the ID.
func ExtractWorkerID(id int64) int64 {
	return (id >> nodeShift) & nodeMask
}

// ExtractSequence decodes the sequence number embedded in the ID.
func ExtractSequence(id int64) int64 {
	return id & stepMask
}
