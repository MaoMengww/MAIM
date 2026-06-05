package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func genKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestEncryptDecrypt(t *testing.T) {
	key := genKey(t)
	plaintext := []byte("sk-this-is-a-test-api-key-12345")

	ciphertext, err := Encrypt(plaintext, key)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, ciphertext)

	decrypted, err := Decrypt(ciphertext, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecryptString(t *testing.T) {
	key := genKey(t)
	plaintext := "sk-test-api-key"

	encoded, err := EncryptString(plaintext, key)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, encoded)

	decoded, err := DecryptString(encoded, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decoded)
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := genKey(t)
	key2 := genKey(t)
	plaintext := []byte("secret")

	ciphertext, err := Encrypt(plaintext, key1)
	require.NoError(t, err)

	_, err = Decrypt(ciphertext, key2)
	assert.Error(t, err)
}

func TestDecryptTruncatedData(t *testing.T) {
	key := genKey(t)
	_, err := Decrypt([]byte("short"), key)
	assert.Error(t, err)
}

func TestEncryptDecryptEmpty(t *testing.T) {
	key := genKey(t)
	plaintext := []byte("")

	ciphertext, err := Encrypt(plaintext, key)
	require.NoError(t, err)

	decrypted, err := Decrypt(ciphertext, key)
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

func TestEncryptDeterministic(t *testing.T) {
	key := genKey(t)
	plaintext := []byte("same-key")

	c1, err := Encrypt(plaintext, key)
	require.NoError(t, err)
	c2, err := Encrypt(plaintext, key)
	require.NoError(t, err)

	// AES-GCM uses random nonce, so outputs should differ
	assert.NotEqual(t, c1, c2)

	// Both should decrypt correctly
	d1, _ := Decrypt(c1, key)
	d2, _ := Decrypt(c2, key)
	assert.Equal(t, plaintext, d1)
	assert.Equal(t, plaintext, d2)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := genKey(t)
	rawKey := base64.StdEncoding.EncodeToString(key)
	assert.Len(t, rawKey, 44) // 32 bytes base64 = 44 chars

	for _, tc := range []struct {
		name string
		data string
	}{
		{"simple", "hello world"},
		{"empty", ""},
		{"unicode", "你好世界 🌍"},
		{"long", string(make([]byte, 10000))},
		{"special chars", `{"key": "value", "nested": {"a": 1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := EncryptString(tc.data, key)
			require.NoError(t, err)
			decoded, err := DecryptString(encoded, key)
			require.NoError(t, err)
			assert.Equal(t, tc.data, decoded)
		})
	}
}
