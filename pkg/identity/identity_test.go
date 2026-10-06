package identity_test

import (
	"testing"

	"github.com/maomeng/aim/pkg/identity"
)

func TestEntityBoundaryRejectsPseudoIdentities(t *testing.T) {
	for _, input := range []string{"", "0", "123", "00000000-0000-0000-0000-000000000000", "019b0123-4567-789a-bcde-f0123456789A", "019b01234567789abcdef0123456789a"} {
		if err := identity.Validate(input); err == nil {
			t.Errorf("accepted invalid entity identity %q", input)
		}
	}
	if err := identity.Validate("019b0123-4567-789a-bcde-f0123456789a"); err != nil {
		t.Fatalf("canonical entity identity rejected: %v", err)
	}
}
