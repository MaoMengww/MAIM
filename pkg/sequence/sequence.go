package sequence

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

// Max is the largest position representable exactly by JavaScript Number.
const Max int64 = 9007199254740991

// Validate checks a position. Zero is a valid initial position, not an entity ID.
func Validate(value int64) error {
	if value < 0 || value > Max {
		return errors.New("sequence outside safe integer range")
	}
	return nil
}

// Next allocates the next position without wrapping. Owners must persist it
// in the same transaction as the record at that position.
func Next(value int64) (int64, error) {
	if err := Validate(value); err != nil {
		return 0, err
	}
	if value == Max {
		return 0, errors.New("sequence exhausted")
	}
	return value + 1, nil
}

// ParseJSON accepts only a JSON number denoting a safe, nonnegative integer.
// It deliberately rejects quoted integers and null rather than supporting an
// old protocol or treating absence as an initial position.
func ParseJSON(raw []byte) (int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return 0, errors.New("invalid sequence JSON")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("sequence must be a JSON number")
	}
	// Parse as an integer without a float conversion or precision loss.
	integer, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil {
		return 0, errors.New("sequence must be an integer")
	}
	if err := Validate(integer); err != nil {
		return 0, err
	}
	return integer, nil
}
