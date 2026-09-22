// Package observability exposes application health and aggregate worker metrics.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Metrics struct {
	Registry  *prometheus.Registry
	Checks    *prometheus.CounterVec
	Errors    *prometheus.CounterVec
	Latency   prometheus.Histogram
	Services  prometheus.Gauge
	LastCycle prometheus.Gauge
}

// New uses a private registry so tests and multiple handlers do not share state.
func New() *Metrics {
	m := &Metrics{
		Registry:  prometheus.NewRegistry(),
		Checks:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "statuspulse_checks_total", Help: "Successfully persisted checks by observed status."}, []string{"status"}),
		Errors:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "statuspulse_worker_errors_total", Help: "Worker operation failures, excluding cancellation and deleted services."}, []string{"operation"}),
		Latency:   prometheus.NewHistogram(prometheus.HistogramOpts{Name: "statuspulse_check_response_duration_seconds", Help: "Time to response headers for persisted checks with an HTTP response.", Buckets: prometheus.DefBuckets}),
		Services:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "statuspulse_services", Help: "Registered services at the last successful worker list operation."}),
		LastCycle: prometheus.NewGauge(prometheus.GaugeOpts{Name: "statuspulse_last_cycle_timestamp_seconds", Help: "Unix time of the last completed worker cycle; zero before completion. Individual checks may have failed."}),
	}
	for _, status := range []string{"UP", "DOWN"} {
		m.Checks.WithLabelValues(status)
	}
	for _, op := range []string{"list", "check", "save"} {
		m.Errors.WithLabelValues(op)
	}
	m.Registry.MustRegister(m.Checks, m.Errors, m.Latency, m.Services, m.LastCycle, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) CycleCompleted() {
	if m != nil {
		m.LastCycle.Set(float64(time.Now().Unix()))
	}
}
