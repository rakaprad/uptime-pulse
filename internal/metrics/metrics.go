// Package metrics defines the Prometheus metrics exported by uptime-pulse.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "uptime_pulse"

// Metrics groups every collector so callers never touch global state.
type Metrics struct {
	registry   *prometheus.Registry
	Up         *prometheus.GaugeVec
	Latency    *prometheus.HistogramVec
	CertExpiry *prometheus.GaugeVec
	Checks     *prometheus.CounterVec
}

// New creates a fresh registry with the uptime-pulse metrics plus the
// standard Go runtime and process collectors.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		Up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "1 if the last check of the target succeeded, 0 otherwise.",
		}, []string{"target"}),
		Latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "check_duration_seconds",
			Help:      "Duration of HTTP checks in seconds.",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"target"}),
		CertExpiry: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "cert_expiry_days",
			Help:      "Days until the target's TLS leaf certificate expires.",
		}, []string{"target"}),
		Checks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "checks_total",
			Help:      "Total number of checks by result (success or failure).",
		}, []string{"target", "result"}),
	}
	m.registry.MustRegister(
		m.Up, m.Latency, m.CertExpiry, m.Checks,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the registry in the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}
