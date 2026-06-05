package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

func Tracing(serviceName string) gin.HandlerFunc {
	tracer := otel.Tracer("aim/gateway")
	propagator := otel.GetTextMapPropagator()

	return func(c *gin.Context) {
		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

		spanName := c.Request.Method + " " + c.FullPath()
		if c.FullPath() == "" {
			spanName = c.Request.Method + " " + c.Request.URL.Path
		}

		ctx, span := tracer.Start(ctx, spanName,
			trace.WithAttributes(
				semconv.HTTPMethodKey.String(c.Request.Method),
				semconv.HTTPURLKey.String(c.Request.URL.String()),
				semconv.HTTPTargetKey.String(c.Request.RequestURI),
				semconv.HTTPHostKey.String(c.Request.Host),
				semconv.HTTPUserAgentKey.String(c.Request.UserAgent()),
			),
		)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		span.SetAttributes(semconv.HTTPStatusCodeKey.Int(c.Writer.Status()))
	}
}
