package execution

import (
	"io"
	"strings"
	"testing"
)

func TestCaptureBudgetCannotBeBypassedByCopy(t *testing.T) {
	capture := &boundedBuffer{limit: 8}
	if _, err := io.Copy(capture, io.LimitReader(strings.NewReader(strings.Repeat("x", 64)), 64)); err == nil {
		t.Fatal("oversized capture accepted")
	}
	if len(capture.Bytes()) > 8 {
		t.Fatal("transport capture exceeded hard budget")
	}
}
