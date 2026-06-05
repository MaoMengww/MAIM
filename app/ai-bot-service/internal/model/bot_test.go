package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilities_ScanValue_RoundTrip(t *testing.T) {
	cap := Capabilities{
		MCPServers: []MCPServerConfig{
			{
				Name:      "weather",
				Transport: "http",
				URL:       "http://localhost:8080",
				TimeoutMs: 10000,
			},
			{
				Name:      "web_search",
				Transport: "http",
				URL:       "http://localhost:8081",
				TimeoutMs: 5000,
			},
		},
	}

	val, err := cap.Value()
	require.NoError(t, err)

	var restored Capabilities
	err = restored.Scan(val)
	require.NoError(t, err)

	assert.Len(t, restored.MCPServers, 2)
	assert.Equal(t, "weather", restored.MCPServers[0].Name)
	assert.Equal(t, "web_search", restored.MCPServers[1].Name)
}

func TestCapabilities_ScanNil(t *testing.T) {
	var cap Capabilities
	err := cap.Scan(nil)
	assert.NoError(t, err)
	assert.Nil(t, cap.MCPServers)
}

func TestCapabilities_ScanEmptyBytes(t *testing.T) {
	var cap Capabilities
	err := cap.Scan([]byte(""))
	assert.Error(t, err)
}

func TestCapabilities_ValueEmpty(t *testing.T) {
	cap := Capabilities{}
	val, err := cap.Value()
	require.NoError(t, err)
	assert.NotNil(t, val)
}
