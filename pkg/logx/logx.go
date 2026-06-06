package logx

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/trace"
	"github.com/zeromicro/go-zero/core/logx"
)

var setupOnce sync.Once

type Logger = logx.Logger

type LogField = logx.LogField

type Config struct {
	Level     string
	Format    string
	Output    string
	FilePath  string
	MaxSize   int
	MaxBackup int
	MaxAge    int
	Compress  bool
	Name      string
	Path      string
}

func NewLogger(cfg Config) Logger {
	setupOnce.Do(func() {
		logCfg := logx.LogConf{
			ServiceName: cfg.Name,
			Level:       cfg.Level,
			Compress:    cfg.Compress,
		}
		if cfg.Format == "json" {
			logCfg.Encoding = "plain"
		}
		switch cfg.Output {
		case "file":
			logCfg.Mode = "file"
			logCfg.Path = cfg.Path
			if cfg.FilePath != "" {
				logCfg.Path = cfg.FilePath
			}
			logCfg.MaxSize = cfg.MaxSize
			logCfg.MaxBackups = cfg.MaxBackup
		default:
			logCfg.Mode = "console"
		}
		logx.MustSetup(logCfg)
	})
	return logx.WithContext(context.Background())
}

func DefaultLogger() Logger {
	return logx.WithContext(context.Background())
}

func Field(key string, value any) LogField {
	return logx.Field(key, value)
}

func Err(err error) LogField {
	if err != nil {
		return logx.Field("error", err.Error())
	}
	return logx.Field("error", nil)
}

func String(key, val string) LogField {
	return logx.Field(key, val)
}

func Int(key string, val int) LogField {
	return logx.Field(key, val)
}

func Any(key string, val any) LogField {
	return logx.Field(key, val)
}

func Fmt(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// WithContext returns a logger with trace_id, span_id, and request_id extracted from ctx.
func WithContext(ctx context.Context, l Logger) Logger {
	fields := make([]LogField, 0, 3)
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		if sc.TraceID().IsValid() {
			fields = append(fields, logx.Field("trace_id", sc.TraceID().String()))
		}
		if sc.SpanID().IsValid() {
			fields = append(fields, logx.Field("span_id", sc.SpanID().String()))
		}
	}
	if v := ctx.Value("request_id"); v != nil {
		if id, ok := v.(string); ok && id != "" {
			fields = append(fields, logx.Field("request_id", id))
		}
	}
	if len(fields) == 0 {
		return l
	}
	return l.WithFields(fields...)
}
