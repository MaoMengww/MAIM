package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/pkg/metrics"
)

var (
	httpRequestsTotal = metrics.NewCounterVec("http_requests_total",
		"Total HTTP requests", "method", "endpoint", "status")
	httpRequestDuration = metrics.NewHistogramVec("http_request_duration_seconds",
		"HTTP request latency in seconds", []string{"method", "endpoint"}, nil)

	httpResponseSize = metrics.NewHistogramVec("http_response_size_bytes",
		"HTTP response size in bytes", []string{"method", "endpoint"}, nil)
	httpActiveRequests = metrics.NewGaugeVec("http_active_requests",
		"Active HTTP requests", "method")
)

func PrometheusMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()
		if path == "" {
			path = "/unknown"
		}

		httpActiveRequests.Add(1, c.Request.Method)
		defer httpActiveRequests.Add(-1, c.Request.Method)

		c.Next()

		status := strconv.Itoa(c.Writer.Status())
		httpRequestsTotal.Inc(c.Request.Method, path, status)
		httpRequestDuration.Observe(time.Since(start).Seconds(), c.Request.Method, path)
		httpResponseSize.Observe(float64(c.Writer.Size()), c.Request.Method, path)
	}
}
