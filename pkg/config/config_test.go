package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadJSON(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"name":"test","port":9090},"redis":{"addr":"localhost:6379"}}`
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0644))

	var cfg Config
	err := Load(tmp, &cfg)
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Server.Name)
	assert.Equal(t, 9090, cfg.Server.Port)
}

func TestLoadWithDefaults(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"name":"test"},"redis":{"addr":"redis:6379"}}`
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0644))

	var cfg Config
	err := Load(tmp, &cfg)
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Server.Name)
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "release", cfg.Server.Mode)
}

func TestLoadWithEnv(t *testing.T) {
	os.Setenv("SERVER_NAME", "env-server")
	defer os.Unsetenv("SERVER_NAME")

	tmp := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"name":"$SERVER_NAME"},"redis":{"addr":"redis:6379"}}`
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0644))

	var cfg Config
	err := Load(tmp, &cfg, WithEnv())
	require.NoError(t, err)
	assert.Equal(t, "env-server", cfg.Server.Name)
}

func TestLoadFileNotFound(t *testing.T) {
	var cfg Config
	err := Load("/nonexistent/config.json", &cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config read failed")
}

func TestLoadUnrecognizedType(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.ini")
	require.NoError(t, os.WriteFile(tmp, []byte("key=val"), 0644))

	var cfg Config
	err := Load(tmp, &cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized")
}

func TestMustLoadPanic(t *testing.T) {
	assert.Panics(t, func() {
		MustLoad("/nonexistent/config.json", &Config{})
	})
}

func TestMustLoadSuccess(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"name":"mustload"},"redis":{"addr":"redis:6379"}}`
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0644))

	var cfg Config
	assert.NotPanics(t, func() {
		MustLoad(tmp, &cfg)
	})
	assert.Equal(t, "mustload", cfg.Server.Name)
}

func TestDefaultValues(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"name":"s"},"redis":{"addr":"r"},"database":{"dsn":"d"}}`
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0644))

	var cfg Config
	err := Load(tmp, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "postgres", cfg.Database.Driver)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, "stdout", cfg.Log.Output)
	assert.Equal(t, "always_on", cfg.Trace.Sampler)
	assert.Equal(t, 1.0, cfg.Trace.Ratio)
	assert.Equal(t, 7200, cfg.JWT.ExpireSec)
	assert.Equal(t, 604800, cfg.JWT.RefreshSec)
	assert.Equal(t, "aim", cfg.MinIO.Bucket)
}

func TestNestedDefaults(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"name":"nested"},"redis":{"addr":"local"},"database":{"dsn":"pg"}}`
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0644))

	var cfg Config
	err := Load(tmp, &cfg)
	require.NoError(t, err)

	assert.Equal(t, 100, cfg.Database.MaxOpenConn)
	assert.Equal(t, 10, cfg.Database.MaxIdleConn)
	assert.Equal(t, 3600, cfg.Database.MaxLifetime)
	assert.Equal(t, 100, cfg.Redis.PoolSize)
	assert.Equal(t, 10, cfg.Redis.MinIdleConn)
}
