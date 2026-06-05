package jwt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate(t *testing.T) {
	m := NewManager("test-secret-key", 3600, 86400)
	token, err := m.Generate("user-001", "mao")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestParse(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	token, err := m.Generate("user-002", "meng")
	require.NoError(t, err)

	claims, err := m.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, "user-002", claims.UserID)
	assert.Equal(t, "meng", claims.Username)
}

func TestParseInvalidToken(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	_, err := m.Parse("invalid.token.here")
	require.Error(t, err)
}

func TestParseWrongSecret(t *testing.T) {
	m1 := NewManager("secret-1", 3600, 86400)
	m2 := NewManager("secret-2", 3600, 86400)

	token, err := m1.Generate("user-003", "test")
	require.NoError(t, err)

	_, err = m2.Parse(token)
	require.Error(t, err)
}

func TestExpiredToken(t *testing.T) {
	m := NewManager("test-secret", 1, 86400)
	token, err := m.Generate("user-004", "expired")
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	_, err = m.Parse(token)
	require.Error(t, err)
}

func TestRefresh(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	token, err := m.Generate("user-005", "refresh")
	require.NoError(t, err)

	newToken, err := m.Refresh(token)
	require.NoError(t, err)
	assert.NotEmpty(t, newToken)
	assert.NotEqual(t, token, newToken)

	claims, err := m.Parse(newToken)
	require.NoError(t, err)
	assert.Equal(t, "user-005", claims.UserID)
}

func TestDefaultValues(t *testing.T) {
	m := NewManager("secret", 0, 0)
	assert.NotNil(t, m)
	assert.Equal(t, 7200, m.expireSec)
	assert.Equal(t, 604800, m.refreshSec)
}

func TestNegativeExpireSec(t *testing.T) {
	m := NewManager("secret", -1, 86400)
	assert.Equal(t, 7200, m.expireSec)
}

func TestNegativeRefreshSec(t *testing.T) {
	m := NewManager("secret", 3600, -1)
	assert.Equal(t, 604800, m.refreshSec)
}

func TestGenerateWithCustomClaims(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	token, err := m.Generate("user-006", "")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestRefreshInvalidToken(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	_, err := m.Refresh("some.invalid.token")
	require.Error(t, err)
}

func TestRefreshExpiredToken(t *testing.T) {
	m := NewManager("test-secret", 1, 86400)
	token, err := m.Generate("user-007", "expired")
	require.NoError(t, err)
	time.Sleep(2 * time.Second)
	_, err = m.Refresh(token)
	require.Error(t, err)
}
