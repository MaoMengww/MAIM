package logx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultLogger(t *testing.T) {
	logger := DefaultLogger()
	assert.NotNil(t, logger)
}

func TestNewLoggerConsole(t *testing.T) {
	logger := NewLogger(Config{
		Level:  "debug",
		Format: "console",
		Output: "stdout",
	})
	assert.NotNil(t, logger)
	logger.Info("test message")
	logger.Infof("formatted %s", "message")
	logger.Debug("debug message")
	logger.Debugf("debug %s", "msg")
}

func TestNewLoggerJSON(t *testing.T) {
	logger := NewLogger(Config{
		Level:  "info",
		Format: "json",
		Output: "stdout",
	})
	assert.NotNil(t, logger)
	logger.Info("json test")
}

func TestLoggerLevels(t *testing.T) {
	logger := NewLogger(Config{
		Level:  "debug",
		Format: "console",
		Output: "stdout",
	})

	logger.Debug("debug")
	logger.Debugf("debugf %d", 1)
	logger.Info("info")
	logger.Infof("infof %d", 2)
	logger.Error("error")
	logger.Errorf("errorf %d", 3)
	logger.Slow("slow")
	logger.Slowf("slowf %d", 4)
}

func TestLoggerWithContext(t *testing.T) {
	logger := NewLogger(Config{
		Level:  "debug",
		Format: "console",
		Output: "stdout",
	})

	ctx := context.WithValue(context.Background(), "trace_id", "trace-123")
	ctx = context.WithValue(ctx, "span_id", "span-456")

	ctxLogger := logger.WithContext(ctx)
	assert.NotNil(t, ctxLogger)
	ctxLogger.Info("contextual log")
}

func TestLoggerWithFields(t *testing.T) {
	logger := NewLogger(Config{
		Level: "debug",
	})
	fieldLogger := logger.WithFields(
		Field("request_id", "req-001"),
		Field("user_id", "user-001"),
	)
	assert.NotNil(t, fieldLogger)
	fieldLogger.Info("field log")
}

func TestLoggerWithDuration(t *testing.T) {
	logger := NewLogger(Config{Level: "debug"})
	durLogger := logger.WithDuration(150 * time.Millisecond)
	assert.NotNil(t, durLogger)
	durLogger.Info("slow operation")
}

func TestLoggerWithCallerSkip(t *testing.T) {
	logger := NewLogger(Config{Level: "debug"})
	skipLogger := logger.WithCallerSkip(2)
	assert.NotNil(t, skipLogger)
	skipLogger.Info("caller skip")
}

func TestLoggerSync(t *testing.T) {
	// go-zero logx.Logger does not expose Sync; the underlying writer is managed by the framework.
	t.Log("Sync is managed by go-zero logx framework")
}

func TestLogFieldHelpers(t *testing.T) {
	assert.Equal(t, "key", Field("key", "val").Key)
	assert.Equal(t, "val", Field("key", "val").Value)

	errField := Err(assert.AnError)
	assert.Equal(t, "error", errField.Key)

	strField := String("name", "john")
	assert.Equal(t, "name", strField.Key)
	assert.Equal(t, "john", strField.Value)

	intField := Int("count", 42)
	assert.Equal(t, "count", intField.Key)
	assert.Equal(t, 42, intField.Value)

	anyField := Any("data", map[string]int{"a": 1})
	assert.Equal(t, "data", anyField.Key)
}

func TestFmt(t *testing.T) {
	result := Fmt("hello %s, age %d", "world", 25)
	assert.Equal(t, "hello world, age 25", result)
}

func TestLevelParsing(t *testing.T) {
	assert.Equal(t, "debug", Fmt("%s", "debug"))

	logger := NewLogger(Config{Level: "warn"})
	assert.NotNil(t, logger)

	logger2 := NewLogger(Config{Level: "error"})
	assert.NotNil(t, logger2)
}

func TestLoggerWithEmptyContext(t *testing.T) {
	logger := NewLogger(Config{Level: "debug"})
	ctxLogger := logger.WithContext(context.Background())
	assert.NotNil(t, ctxLogger)
	ctxLogger.Info("no trace context")
}
