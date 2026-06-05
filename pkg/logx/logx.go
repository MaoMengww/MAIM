package logx

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger interface {
	Debug(...any)
	Debugf(string, ...any)
	Info(...any)
	Infof(string, ...any)
	Error(...any)
	Errorf(string, ...any)
	Slow(...any)
	Slowf(string, ...any)
	WithContext(ctx context.Context) Logger
	WithFields(fields ...LogField) Logger
	WithDuration(d time.Duration) Logger
	WithCallerSkip(skip int) Logger
	Sync() error
}

type LogField struct {
	Key   string
	Value any
}

type logger struct {
	zap   *zap.Logger
	sugar *zap.SugaredLogger
}

type Config struct {
	Level     string
	Format    string
	Output    string
	FilePath  string
	MaxSize   int
	MaxBackup int
	MaxAge    int
	Compress  bool
	// Path enables go-zero-compatible file logging to {Path}/{Name}.log.
	// When Path is set, it takes precedence over FilePath.
	Name string
	Path string
}

func NewLogger(cfg Config) Logger {
	level := parseLevel(cfg.Level)
	encoder := createEncoder(cfg.Format)
	writer := createWriter(cfg)
	core := zapcore.NewCore(encoder, writer, level)
	zapLogger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	return &logger{
		zap:   zapLogger,
		sugar: zapLogger.Sugar(),
	}
}

func DefaultLogger() Logger {
	return NewLogger(Config{
		Level:  "debug",
		Format: "console",
		Output: "stdout",
	})
}

func parseLevel(l string) zapcore.Level {
	switch l {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func createEncoder(format string) zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
	}
	if format == "json" {
		return zapcore.NewJSONEncoder(cfg)
	}
	cfg.EncodeLevel = colorLevelEncoder
	return zapcore.NewConsoleEncoder(cfg)
}

func colorLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	c, ok := _levelToColor[l]
	if !ok {
		c = _levelToColor[zapcore.ErrorLevel]
	}
	enc.AppendString(WithColor(l.CapitalString(), c))
}

var _levelToColor = map[zapcore.Level]*color.Color{
	zapcore.DebugLevel:  color.New(color.FgCyan),
	zapcore.InfoLevel:   color.New(color.FgGreen),
	zapcore.WarnLevel:   color.New(color.FgYellow),
	zapcore.ErrorLevel:  color.New(color.FgRed),
	zapcore.DPanicLevel: color.New(color.FgMagenta),
	zapcore.PanicLevel:  color.New(color.FgWhite, color.BgRed),
	zapcore.FatalLevel:  color.New(color.FgWhite, color.BgRed),
}

// WithColor 在纯文本编码时，给字符串添加颜色。
func WithColor(text string, colour *color.Color) string {
	return colour.Sprint(text)
}

func createWriter(cfg Config) zapcore.WriteSyncer {
	filename := cfg.FilePath
	if cfg.Path != "" {
		name := cfg.Name
		if name == "" {
			name = "service"
		}
		filename = cfg.Path + "/" + name + ".log"
	}
	if filename != "" {
		lj := &lumberjack.Logger{
			Filename:   filename,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackup,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
		return zapcore.AddSync(lj)
	}
	return zapcore.AddSync(os.Stdout)
}

func (l *logger) Debug(args ...any) {
	l.sugar.Debug(args...)
}

func (l *logger) Debugf(template string, args ...any) {
	l.sugar.Debugf(template, args...)
}

func (l *logger) Info(args ...any) {
	l.sugar.Info(args...)
}

func (l *logger) Infof(template string, args ...any) {
	l.sugar.Infof(template, args...)
}

func (l *logger) Error(args ...any) {
	l.sugar.Error(args...)
}

func (l *logger) Errorf(template string, args ...any) {
	l.sugar.Errorf(template, args...)
}

func (l *logger) Slow(args ...any) {
	l.sugar.Warn(args...)
}

func (l *logger) Slowf(template string, args ...any) {
	l.sugar.Warnf(template, args...)
}

func (l *logger) WithContext(ctx context.Context) Logger {
	newZap := l.zap.With(
		zap.String("trace_id", getTraceID(ctx)),
		zap.String("span_id", getSpanID(ctx)),
		zap.String("request_id", getRequestID(ctx)),
	)
	return &logger{zap: newZap, sugar: newZap.Sugar()}
}

func (l *logger) WithFields(fields ...LogField) Logger {
	zapFields := make([]zap.Field, 0, len(fields))
	for _, f := range fields {
		zapFields = append(zapFields, zap.Any(f.Key, f.Value))
	}
	newZap := l.zap.With(zapFields...)
	return &logger{zap: newZap, sugar: newZap.Sugar()}
}

func (l *logger) WithDuration(d time.Duration) Logger {
	newZap := l.zap.With(zap.Duration("duration", d))
	return &logger{zap: newZap, sugar: newZap.Sugar()}
}

func (l *logger) WithCallerSkip(skip int) Logger {
	newZap := l.zap.WithOptions(zap.AddCallerSkip(skip))
	return &logger{zap: newZap, sugar: newZap.Sugar()}
}

func (l *logger) Sync() error {
	return l.zap.Sync()
}

func getTraceID(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() && sc.TraceID().IsValid() {
		return sc.TraceID().String()
	}
	if v := ctx.Value("trace_id"); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

func getSpanID(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() && sc.SpanID().IsValid() {
		return sc.SpanID().String()
	}
	if v := ctx.Value("span_id"); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

func getRequestID(ctx context.Context) string {
	if v := ctx.Value("request_id"); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	// Fall back to trace_id for async flows (e.g. Kafka consumers) where no HTTP request_id exists.
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() && sc.TraceID().IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

func Field(key string, value any) LogField {
	return LogField{Key: key, Value: value}
}

func Err(err error) LogField {
	if err != nil {
		return LogField{Key: "error", Value: err.Error()}
	}
	return LogField{Key: "error", Value: nil}
}

func String(key, val string) LogField {
	return LogField{Key: key, Value: val}
}

func Int(key string, val int) LogField {
	return LogField{Key: key, Value: val}
}

func Any(key string, val any) LogField {
	return LogField{Key: key, Value: val}
}

func Fmt(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
