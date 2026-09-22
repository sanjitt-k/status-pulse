package monitor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"statuspulse/internal/observability"
	"statuspulse/internal/store"
)

type Scheduler struct {
	// Metrics is optional and must be set before Run starts.
	Metrics  *observability.Metrics
	store    store.ServiceStore
	checker  *Checker
	interval time.Duration
}

func NewScheduler(serviceStore store.ServiceStore, checker *Checker, interval time.Duration) *Scheduler {
	return &Scheduler{store: serviceStore, checker: checker, interval: interval}
}

// Run performs a startup cycle and then checks on ticks. Cycles are synchronous
// and never overlap; slow cycles may cause ticks to be dropped.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.run(ctx, ticker.C)
}

// A supplied tick channel lets tests drive scheduling without clock sleeps.
func (s *Scheduler) run(ctx context.Context, ticks <-chan time.Time) {
	for {
		if ctx.Err() != nil {
			return
		}
		s.runCycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context) {
	services, err := s.store.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			if s.Metrics != nil {
				s.Metrics.Errors.WithLabelValues("list").Inc()
			}
			slog.Error("list services for monitoring", "error", err)
		}
		return
	}
	if s.Metrics != nil {
		s.Metrics.Services.Set(float64(len(services)))
	}
	for _, service := range services {
		if ctx.Err() != nil {
			return
		}
		result, err := s.checker.Check(ctx, service)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if s.Metrics != nil {
				s.Metrics.Errors.WithLabelValues("check").Inc()
			}
			slog.Error("check service", "service_id", service.ID, "error", err)
			continue
		}
		if err := s.store.SaveCheck(ctx, result); err != nil {
			if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
				if s.Metrics != nil {
					s.Metrics.Errors.WithLabelValues("save").Inc()
				}
				slog.Error("save check", "service_id", service.ID, "error", err)
			}
			continue
		}
		if s.Metrics != nil {
			s.Metrics.Checks.WithLabelValues(string(result.Status)).Inc()
			if result.ResponseTimeMS != nil {
				s.Metrics.Latency.Observe(float64(*result.ResponseTimeMS) / 1000)
			}
		}
		slog.Info("check recorded", "service_id", service.ID, "status", result.Status, "http_status_code", result.HTTPStatusCode, "response_time_ms", result.ResponseTimeMS, "error_kind", result.ErrorKind)
	}
	if ctx.Err() == nil {
		s.Metrics.CycleCompleted()
	}
}
