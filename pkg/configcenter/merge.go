package configcenter

import (
	"encoding/json"
	"fmt"
)

// MergeRemote merges remote JSON config into the local config struct.
// Strategy: remote fields override local fields at all levels (deep merge).
// Only JSON-tagged fields participate in the merge.
func MergeRemote(local any, remoteJSON []byte) error {
	localJSON, err := json.Marshal(local)
	if err != nil {
		return fmt.Errorf("merge marshal local: %w", err)
	}

	var merged map[string]any
	if err := json.Unmarshal(localJSON, &merged); err != nil {
		return fmt.Errorf("merge unmarshal local: %w", err)
	}

	var remote map[string]any
	if err := json.Unmarshal(remoteJSON, &remote); err != nil {
		return fmt.Errorf("merge unmarshal remote: %w", err)
	}

	deepMerge(merged, remote)

	result, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("merge marshal result: %w", err)
	}

	if err := json.Unmarshal(result, local); err != nil {
		return fmt.Errorf("merge apply: %w", err)
	}

	return nil
}

// deepMerge recursively merges src into dst. Scalar values, slices, and
// maps from src override dst. Nested maps are merged recursively.
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		srcMap, srcOk := v.(map[string]any)
		dstMap, dstOk := dst[k].(map[string]any)
		if srcOk && dstOk {
			deepMerge(dstMap, srcMap)
			continue
		}
		dst[k] = v
	}
}

// MarshalToJSONMap converts any struct to a map[string]any via JSON.
func MarshalToJSONMap(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
