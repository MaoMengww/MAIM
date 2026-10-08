package messageservicelogic

import (
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/pkg/identity"
)

// Output metadata is part of every consumer's message contract, not opaque
// executor text. Validate once here so REST, WS and Webhook cannot poison sync.
func validateBotMetadata(raw string) error {
	if raw == "" {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return fmt.Errorf("bot metadata must be a JSON object")
	}
	// These are display links only. Identity and current sending permission still
	// come from the authenticated call and authoritative conversation binding.
	for _, field := range []string{"run_id", "interaction_id"} {
		if value, exists := object[field]; exists {
			var id string
			if err := json.Unmarshal(value, &id); err != nil || identity.Validate(id) != nil {
				return fmt.Errorf("invalid bot metadata %s", field)
			}
		}
	}
	sourceJSON, exists := object["kb_sources"]
	if !exists || string(sourceJSON) == "null" {
		return nil
	}
	var sources []map[string]json.RawMessage
	if err := json.Unmarshal(sourceJSON, &sources); err != nil {
		return fmt.Errorf("knowledge sources must be an array")
	}
	for _, source := range sources {
		for _, field := range []string{"type", "kb_name", "title", "content", "kb_id", "doc_id", "chunk_id"} {
			var text *string
			if err := json.Unmarshal(source[field], &text); err != nil || text == nil {
				return fmt.Errorf("invalid knowledge source %s", field)
			}
			value := *text
			switch field {
			case "type":
				if value != "rag" {
					return fmt.Errorf("invalid knowledge source type")
				}
			case "kb_id", "doc_id", "chunk_id":
				if identity.Validate(value) != nil {
					return fmt.Errorf("invalid knowledge source %s", field)
				}
			}
		}
	}
	return nil
}
