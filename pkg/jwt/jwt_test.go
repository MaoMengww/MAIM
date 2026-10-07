package jwt

import (
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testUserID = "01963d97-bb37-7bea-a746-727bcc312b6f"
const testSessionID = "01963d97-bb37-7bea-a746-727bcc312b70"

func TestParse(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	token, err := m.Generate(testUserID, "meng", "browser-device", testSessionID)
	require.NoError(t, err)
	claims, err := m.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, testUserID, claims.UserID)
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
	token, err := m1.Generate(testUserID, "test", "browser-device", testSessionID)
	require.NoError(t, err)
	_, err = m2.Parse(token)
	require.Error(t, err)
}

func TestExpiredToken(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	token := signedUserToken(t, testUserID, time.Now().Add(-time.Hour))
	_, err := m.Parse(token)
	require.Error(t, err)
	claims, err := m.ParseIgnoreExpiry(token)
	require.NoError(t, err)
	assert.Equal(t, testUserID, claims.UserID)
}

func TestRefresh(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	token, err := m.Generate(testUserID, "refresh", "browser-device", testSessionID)
	require.NoError(t, err)
	newToken, err := m.Refresh(token)
	require.NoError(t, err)
	oldClaims, err := m.Parse(token)
	require.NoError(t, err)
	claims, err := m.Parse(newToken)
	require.NoError(t, err)
	assert.Equal(t, testUserID, claims.UserID)
	assert.NotEqual(t, oldClaims.ID, claims.ID)
}

func TestRefreshInvalidToken(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	_, err := m.Refresh("some.invalid.token")
	require.Error(t, err)
}

func TestRefreshExpiredToken(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	_, err := m.Refresh(signedUserToken(t, testUserID, time.Now().Add(-time.Hour)))
	require.Error(t, err)
}

func TestInvalidUserIdentity(t *testing.T) {
	m := NewManager("test-secret", 3600, 86400)
	for _, id := range []string{"", "123", "0", "00000000-0000-0000-0000-000000000000", "01963D97-BB37-7BEA-A746-727BCC312B6F"} {
		t.Run(id, func(t *testing.T) {
			_, err := m.Generate(id, "invalid", "browser-device", testSessionID)
			require.Error(t, err)
			token := signedUserToken(t, id, time.Now().Add(time.Hour))
			_, err = m.Parse(token)
			require.Error(t, err)
			_, err = m.ParseIgnoreExpiry(token)
			require.Error(t, err)
		})
	}
}

func signedUserToken(t *testing.T, userID string, expires time.Time) string {
	t.Helper()
	claims := Claims{UserID: userID, DeviceID: "browser-device", SessionID: testSessionID, RegisteredClaims: jwtlib.RegisteredClaims{Subject: userID, ExpiresAt: jwtlib.NewNumericDate(expires)}}
	token, err := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
	require.NoError(t, err)
	return token
}
