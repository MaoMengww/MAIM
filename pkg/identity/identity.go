package identity

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// New allocates an entity identity in its owning domain. UUID time bits are
// not a conversation sequence, sync position, or publication order.
func New() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate entity identity: %w", err)
	}
	return id.String(), nil
}

// Validate accepts only canonical, nonzero RFC UUIDs at protocol boundaries.
// New entities must use New; validation does not infer allocation ownership.
func Validate(value string) error {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.Variant() != uuid.RFC4122 || id.String() != value {
		return errors.New("invalid entity identity: expected canonical nonzero UUID")
	}
	return nil
}

// Normalize explicitly canonicalizes a standard hyphenated UUID. Public
// protocol decoders use Validate instead: no alternate wire representations.
func Normalize(value string) (string, error) {
	if len(value) != 36 {
		return "", errors.New("invalid entity identity")
	}
	value = strings.ToLower(value)
	if err := Validate(value); err != nil {
		return "", err
	}
	return value, nil
}

// ValidateSubmissionKey checks the client's UUIDv4 sending-action identity.
// It is not the final message ID and must be reused for retries of that action.
func ValidateSubmissionKey(value string) error {
	if err := Validate(value); err != nil {
		return err
	}
	id, _ := uuid.Parse(value)
	if id.Version() != 4 {
		return errors.New("invalid client submission key: expected UUIDv4")
	}
	return nil
}
