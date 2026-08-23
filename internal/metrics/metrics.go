// Package metrics contains Gatekeeper's Prometheus instrumentation.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns Gatekeeper's collectors and their isolated registry.
type Metrics struct {
	requests      *prometheus.CounterVec
	duration      *prometheus.HistogramVec
	limiterErrors *prometheus.CounterVec
	handler       http.Handler
}

// New constructs an independent registry and registers Gatekeeper's metrics.
func New() *Metrics {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "gatekeeper",
		Name:      "requests_total",
		Help:      "Number of matched gateway requests by route and result.",
	}, []string{"route", "result"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "gatekeeper",
		Name:      "request_duration_seconds",
		Help:      "Matched gateway request duration in seconds by route and result.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"route", "result"})
	limiterErrors := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "gatekeeper",
		Name:      "limiter_errors_total",
		Help:      "Number of rate-limiter dependency errors by route.",
	}, []string{"route"})

	registry.MustRegister(requests, duration, limiterErrors)
	return &Metrics{
		requests:      requests,
		duration:      duration,
		limiterErrors: limiterErrors,
		handler:       promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	}
}

// ObserveRequest records one completed matched request.
func (m *Metrics) ObserveRequest(route, result string, elapsed time.Duration) {
	m.requests.WithLabelValues(route, result).Inc()
	m.duration.WithLabelValues(route, result).Observe(elapsed.Seconds())
}

// IncLimiterError records one failed rate-limiter operation.
func (m *Metrics) IncLimiterError(route string) {
	m.limiterErrors.WithLabelValues(route).Inc()
}

// Handler serves this Metrics instance's Prometheus registry.
func (m *Metrics) Handler() http.Handler {
	return m.handler
}
