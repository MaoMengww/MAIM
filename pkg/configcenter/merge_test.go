package configcenter

import (
	"encoding/json"
	"testing"
)

type testNested struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type testConfig struct {
	Name      string       `json:"name"`
	Port      int          `json:"port"`
	RateLimit int          `json:"rateLimit"`
	Nested    testNested   `json:"nested"`
	Tags      []string     `json:"tags"`
}

func TestMergeRemote(t *testing.T) {
	local := &testConfig{
		Name:      "gateway",
		Port:      8080,
		RateLimit: 100,
		Nested: testNested{
			Key:   "k1",
			Value: "v1",
		},
		Tags: []string{"a", "b"},
	}

	remote := map[string]any{
		"rateLimit": 200,
		"nested": map[string]any{
			"value": "v2",
		},
		"tags": []string{"c", "d"},
	}

	remoteJSON, _ := json.Marshal(remote)

	if err := MergeRemote(local, remoteJSON); err != nil {
		t.Fatalf("MergeRemote failed: %v", err)
	}

	if local.Name != "gateway" {
		t.Errorf("Name = %q, want %q", local.Name, "gateway")
	}
	if local.Port != 8080 {
		t.Errorf("Port = %d, want %d", local.Port, 8080)
	}
	if local.RateLimit != 200 {
		t.Errorf("RateLimit = %d, want %d", local.RateLimit, 200)
	}
	if local.Nested.Key != "k1" {
		t.Errorf("Nested.Key = %q, want %q", local.Nested.Key, "k1")
	}
	if local.Nested.Value != "v2" {
		t.Errorf("Nested.Value = %q, want %q", local.Nested.Value, "v2")
	}
	if len(local.Tags) != 2 || local.Tags[0] != "c" || local.Tags[1] != "d" {
		t.Errorf("Tags = %v, want [c d]", local.Tags)
	}
}

func TestMergeRemoteEmpty(t *testing.T) {
	local := &testConfig{Name: "gw", Port: 8080}

	if err := MergeRemote(local, nil); err == nil {
		t.Error("expected error for nil remote")
	}
}

func TestMergeRemoteNoOverlap(t *testing.T) {
	local := &testConfig{Name: "gw", Port: 8080, RateLimit: 100}

	remote := []byte(`{"extraField": "value"}`)
	if err := MergeRemote(local, remote); err != nil {
		t.Fatalf("MergeRemote failed: %v", err)
	}
	if local.Name != "gw" || local.Port != 8080 || local.RateLimit != 100 {
		t.Errorf("local should be unchanged, got %+v", local)
	}
}
