package snowflake

import (
	"fmt"
	"sync"
	"time"

	"github.com/maomeng/aim/pkg/errors"
)

const (
	epoch          int64 = 1700000000000
	workerBits     uint8 = 10
	sequenceBits   uint8 = 12
	workerMax      int64 = -1 ^ (-1 << workerBits)
	sequenceMax    int64 = -1 ^ (-1 << sequenceBits)
	maxRollbackMs  int64 = 500 // 最大容忍时钟回拨，超过则拒绝生成
	workerShift    uint8 = sequenceBits
	timestampShift uint8 = sequenceBits + workerBits
)

type Node struct {
	mu       sync.Mutex
	epoch    int64
	workerID int64
	sequence int64
	lastMs   int64
}

func NewNode(workerID int64) (*Node, error) {
	if workerID < 0 || workerID > workerMax {
		return nil, fmt.Errorf("worker ID must be between 0 and %d", workerMax)
	}
	return &Node{
		epoch:    epoch,
		workerID: workerID,
	}, nil
}

func (n *Node) Generate() (int64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	now := time.Now().UnixMilli()
	if now < n.lastMs {
		offset := n.lastMs - now
		if offset > maxRollbackMs {
			return 0, fmt.Errorf("clock rollback too large: %dms", offset)
		}
		// offset <= 500ms: sleep 等待时钟追上
		time.Sleep(time.Duration(offset) * time.Millisecond)
		now = time.Now().UnixMilli()
		if now < n.lastMs {
			now = n.waitNextMs(n.lastMs)
		}
	}

	if now == n.lastMs {
		n.sequence = (n.sequence + 1) & sequenceMax
		if n.sequence == 0 {
			now = n.waitNextMs(n.lastMs)
		}
	} else {
		n.sequence = 0
	}

	n.lastMs = now
	id := (now-n.epoch)<<timestampShift |
		n.workerID<<workerShift |
		n.sequence
	return id, nil
}

func (n *Node) GenerateString() (string, error) {
	id, err := n.Generate()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", id), nil
}

func (n *Node) GenerateWithError() (int64, error) {
	if n == nil {
		return 0, errors.New(1006, "snowflake node is nil")
	}
	return n.Generate()
}

func (n *Node) waitNextMs(lastMs int64) int64 {
	now := time.Now().UnixMilli()
	for now <= lastMs {
		now = time.Now().UnixMilli()
	}
	return now
}

func ExtractTimestamp(id int64) time.Time {
	ts := (id >> timestampShift) + epoch
	return time.UnixMilli(ts)
}

func ExtractWorkerID(id int64) int64 {
	return (id >> workerShift) & workerMax
}

func ExtractSequence(id int64) int64 {
	return id & sequenceMax
}
