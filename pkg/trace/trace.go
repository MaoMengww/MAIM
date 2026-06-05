package trace

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type Config struct {
	Name     string
	Endpoint string
	Sampler  float64
	Disabled bool
}

func InitTracerProvider(cfg Config) (*sdktrace.TracerProvider, error) {
	if cfg.Disabled {
		return nil, nil
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	exp, err := otlptracegrpc.New(
		context.Background(),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("otel exporter: %w", err)
	}

	sampler := sdktrace.AlwaysSample()
	if cfg.Sampler > 0 && cfg.Sampler < 1.0 {
		sampler = sdktrace.TraceIDRatioBased(cfg.Sampler)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(cfg.Name),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sampler)),
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	return tp, nil
}
