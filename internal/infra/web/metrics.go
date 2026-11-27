package web

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metrics are the server's Prometheus metrics, on a registry of their own so
// tests can build many servers.
type metrics struct {
	registry *prometheus.Registry
	actions  *prometheus.CounterVec
	running  prometheus.Gauge
	syncs    *prometheus.CounterVec
}

// newMetrics registers the metrics and the Go and process collectors.
func newMetrics() *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		//nolint:exhaustruct_v5 // the other options are for subsystems and constant labels
		actions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wpfleet_actions_total",
			Help: "Actions people asked for, by action and result (ok, failed, refused, busy).",
		}, []string{"action", "result"}),
		//nolint:exhaustruct_v5 // as above
		running: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wpfleet_actions_running",
			Help: "Actions running in the background now.",
		}),
		//nolint:exhaustruct_v5 // as above
		syncs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wpfleet_git_syncs_total",
			Help: "Syncs of the fleet clone started by the webhook, by result.",
		}, []string{"result"}),
	}

	m.registry.MustRegister(
		m.actions, m.running, m.syncs,
		collectors.NewGoCollector(),
		//nolint:exhaustruct_v5 // the zero options collect this process's own metrics
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return m
}

// handler serves the registry.
func (m *metrics) handler() http.Handler {
	//nolint:exhaustruct_v5 // the zero options are promhttp's defaults
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
