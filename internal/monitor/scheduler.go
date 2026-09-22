package monitor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"statuspulse/internal/store"
)

type Scheduler struct {
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
	for {
		s.runCycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context) {
	services, err := s.store.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("list services for monitoring", "error", err)
		}
		return
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
			slog.Error("check service", "service_id", service.ID, "error", err)
			continue
		}
		if err := s.store.SaveCheck(ctx, result); err != nil && !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			slog.Error("save check", "service_id", service.ID, "error", err)
		}
	}
}
