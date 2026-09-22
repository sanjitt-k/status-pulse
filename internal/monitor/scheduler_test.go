package monitor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker")
	}
}

func TestSchedulerTicksNeverOverlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	checker := NewChecker(5 * time.Second)
	checker.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	s := store.NewMemoryStore()
	if _, err := s.Create(ctx, model.Service{URL: "http://local.test"}); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); NewScheduler(s, checker, time.Minute).run(ctx, ticks) }()
	defer func() { cancel(); awaitSignal(t, done); checker.Close() }()
	awaitSignal(t, started)
	// While the first request is blocked, the scheduler must not receive another tick.
	select {
	case ticks <- time.Now():
		t.Fatal("scheduler accepted overlapping work")
	default:
	}
	close(release)
	select {
	case ticks <- time.Now():
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not finish first cycle")
	}
	awaitSignal(t, started)
}

type failingSaveStore struct {
	store.ServiceStore
	saves int
}

func (s *failingSaveStore) SaveCheck(ctx context.Context, c model.HealthCheck) error {
	s.saves++
	if s.saves == 1 {
		return errors.New("injected write failure")
	}
	return s.ServiceStore.SaveCheck(ctx, c)
}

func TestSchedulerContinuesAfterSaveFailure(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer endpoint.Close()
	memory := store.NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := memory.Create(ctx, model.Service{URL: endpoint.URL}); err != nil {
			t.Fatal(err)
		}
	}
	s := &failingSaveStore{ServiceStore: memory}
	checker := NewChecker(time.Second)
	defer checker.Close()
	NewScheduler(s, checker, time.Minute).runCycle(ctx)
	checks, err := memory.ListChecks(ctx, 2, 50, 0)
	if err != nil || len(checks) != 1 || s.saves != 2 {
		t.Fatalf("worker stopped after error: %+v %v", checks, err)
	}
}

func TestDeletionDuringSuccessfulCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer endpoint.Close()
	s := store.NewMemoryStore()
	service, err := s.Create(ctx, model.Service{URL: endpoint.URL})
	if err != nil {
		t.Fatal(err)
	}
	checker := NewChecker(time.Second)
	defer checker.Close()
	done := make(chan struct{})
	go func() { defer close(done); NewScheduler(s, checker, time.Minute).runCycle(ctx) }()
	defer func() { cancel(); awaitSignal(t, done) }()
	awaitSignal(t, started)
	if err := s.Delete(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitSignal(t, done)
	if _, err := s.ListChecks(ctx, service.ID, 50, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted service regained history: %v", err)
	}
}
