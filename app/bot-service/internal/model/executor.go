package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ParseExecutorCapabilities rejects unsupported input formats; control abilities
// are handled by the platform's durable input and control delivery contract.
func ParseExecutorCapabilities(raw []byte) (ExecutorCapabilities, error) {
	var caps ExecutorCapabilities
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return caps, err
	}
	for _, field := range []string{"input_types", "supports_input", "supports_stop", "supports_resume"} {
		value, ok := fields[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return caps, fmt.Errorf("executor capability %s must be explicit", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&caps); err != nil {
		return caps, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return caps, fmt.Errorf("trailing executor capability content")
	}
	if len(caps.InputTypes) != 1 || caps.InputTypes[0] != "text" {
		return caps, fmt.Errorf("executor must declare input_types [text]")
	}
	return caps, nil
}
