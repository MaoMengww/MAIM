package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/zeromicro/go-zero/core/metric"
)

var defaultNamespace = "aim"

func SetNamespace(ns string) {
	defaultNamespace = ns
}

type CounterVec struct {
	vec metric.CounterVec
}

func NewCounterVec(name, help string, labels ...string) *CounterVec {
	cv := metric.NewCounterVec(&metric.CounterVecOpts{
		Namespace: defaultNamespace,
		Subsystem: "service",
		Name:      name,
		Help:      help,
		Labels:    labels,
	})
	return &CounterVec{vec: cv}
}

func (c *CounterVec) Inc(labels ...string) {
	c.vec.Inc(labels...)
}

func (c *CounterVec) Add(v float64, labels ...string) {
	c.vec.Add(v, labels...)
}

type GaugeVec struct {
	vec metric.GaugeVec
}

func NewGaugeVec(name, help string, labels ...string) *GaugeVec {
	gv := metric.NewGaugeVec(&metric.GaugeVecOpts{
		Namespace: defaultNamespace,
		Subsystem: "service",
		Name:      name,
		Help:      help,
		Labels:    labels,
	})
	return &GaugeVec{vec: gv}
}

func (g *GaugeVec) Set(v float64, labels ...string) {
	g.vec.Set(v, labels...)
}

func (g *GaugeVec) Add(v float64, labels ...string) {
	g.vec.Add(v, labels...)
}

type HistogramVec struct {
	vec metric.HistogramVec
}

func NewHistogramVec(name, help string, labels []string, buckets []float64) *HistogramVec {
	if len(buckets) == 0 {
		buckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
	}
	hv := metric.NewHistogramVec(&metric.HistogramVecOpts{
		Namespace: defaultNamespace,
		Subsystem: "service",
		Name:      name,
		Help:      help,
		Labels:    labels,
		Buckets:   buckets,
	})
	return &HistogramVec{vec: hv}
}

func (h *HistogramVec) Observe(v float64, labels ...string) {
	h.vec.ObserveFloat(v, labels...)
}

func Handler() http.Handler {
	return promhttp.Handler()
}
