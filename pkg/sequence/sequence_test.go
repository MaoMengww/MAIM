package sequence_test

import (
	"testing"

	"github.com/maomeng/aim/pkg/sequence"
)

func TestSafeSequenceJSONBoundary(t *testing.T) {
	for _, input := range []string{`"1"`, `-1`, `1.5`, `9007199254740992`, `null`, `true`} {
		if _, err := sequence.ParseJSON([]byte(input)); err == nil {
			t.Errorf("accepted invalid sequence %s", input)
		}
	}
	got, err := sequence.ParseJSON([]byte(`9007199254740991`))
	if err != nil || got != 9007199254740991 {
		t.Fatalf("safe upper bound: got %d, error %v", got, err)
	}
	if _, err := sequence.Next(got); err == nil {
		t.Fatal("exhausted sequence wrapped or advanced")
	}
}
