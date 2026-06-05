package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCounterVec(t *testing.T) {
	cv := NewCounterVec("test_total", "Test counter", "method")
	assert.NotNil(t, cv)
	assert.NotNil(t, cv.vec)
	cv.Inc("GET")
	cv.Add(5, "POST")
}

func TestNewGaugeVec(t *testing.T) {
	gv := NewGaugeVec("test_gauge", "Test gauge", "service")
	assert.NotNil(t, gv)
	assert.NotNil(t, gv.vec)
	gv.Set(1.0, "api")
	gv.Add(0.5, "api")
}

func TestNewHistogramVec(t *testing.T) {
	hv := NewHistogramVec("test_seconds", "Test histogram", []string{"method"}, nil)
	assert.NotNil(t, hv)
	assert.NotNil(t, hv.vec)
	hv.Observe(0.1, "GET")
	hv.Observe(0.5, "POST")
}

func TestNewHistogramVecCustomBuckets(t *testing.T) {
	buckets := []float64{1, 5, 10}
	hv := NewHistogramVec("test_custom", "Custom buckets", []string{"path"}, buckets)
	assert.NotNil(t, hv)
}

func TestSetNamespace(t *testing.T) {
	SetNamespace("custom")
	cv := NewCounterVec("ns_test", "NS test")
	assert.NotNil(t, cv)
	SetNamespace("aim")
}

func TestHandler(t *testing.T) {
	h := Handler()
	assert.NotNil(t, h)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMultipleMetrics(t *testing.T) {
	reqCounter := NewCounterVec("requests_total", "Request count", "method", "status")
	connGauge := NewGaugeVec("connections", "Active connections", "service")
	latency := NewHistogramVec("latency_seconds", "Request latency", []string{"method"}, nil)

	reqCounter.Inc("GET", "200")
	reqCounter.Inc("POST", "201")
	connGauge.Set(42, "gateway")
	latency.Observe(0.05, "GET")
	latency.Observe(0.15, "POST")

	assert.NotNil(t, reqCounter)
	assert.NotNil(t, connGauge)
	assert.NotNil(t, latency)
}

func TestCounterVecConcurrent(t *testing.T) {
	cv := NewCounterVec("concurrent_total", "Concurrent test", "label")

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				cv.Inc("a")
				cv.Add(1, "b")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}
