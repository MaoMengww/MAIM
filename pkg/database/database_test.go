package database

import (
	"testing"

	"github.com/maomeng/aim/pkg/config"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/stretchr/testify/assert"
)

func TestNewDBEmptyDSN(t *testing.T) {
	cfg := config.DatabaseConfig{Driver: "postgres", DSN: ""}
	log := logx.DefaultLogger()
	db, err := NewDB(cfg, log)
	assert.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "DSN is required")
}

func TestNewDBUnsupportedDriver(t *testing.T) {
	cfg := config.DatabaseConfig{Driver: "mysql", DSN: "localhost:3306"}
	log := logx.DefaultLogger()
	db, err := NewDB(cfg, log)
	assert.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestNewDBInvalidDSN(t *testing.T) {
	cfg := config.DatabaseConfig{Driver: "postgres", DSN: "host=invalid port=5432"}
	log := logx.DefaultLogger()
	_, err := NewDB(cfg, log)
	assert.Error(t, err)
}
