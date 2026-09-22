package monitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/observability"
	"statuspulse/internal/store"
)

type failingMetricsStore struct{ store.ServiceStore }

func (s failingMetricsStore) SaveCheck(context.Context, model.HealthCheck) error {
	return errors.New("storage failure")
}

func TestMetricsDoNotCountFailedPersistence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	s := store.NewMemoryStore()
	if _, err := s.Create(context.Background(), model.Service{Name: "test", URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	c := NewChecker(time.Second)
	defer c.Close()
	scheduler := NewScheduler(failingMetricsStore{s}, c, time.Minute)
	scheduler.Metrics = observability.New()
	scheduler.runCycle(context.Background())
	w := httptest.NewRecorder()
	scheduler.Metrics.Handler(http.NotFoundHandler(), func(context.Context) error { return nil }).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{`statuspulse_checks_total{status="UP"} 0`, `statuspulse_worker_errors_total{operation="save"} 1`, `statuspulse_check_response_duration_seconds_count 0`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestMetricsCountPersistedChecksAndIgnoreCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	s := store.NewMemoryStore()
	if _, err := s.Create(context.Background(), model.Service{Name: "test", URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	c := NewChecker(time.Second)
	defer c.Close()
	scheduler := NewScheduler(s, c, time.Minute)
	scheduler.Metrics = observability.New()
	scheduler.runCycle(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler.runCycle(ctx)
	w := httptest.NewRecorder()
	scheduler.Metrics.Handler(http.NotFoundHandler(), func(context.Context) error { return nil }).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{`statuspulse_checks_total{status="DOWN"} 1`, `statuspulse_checks_total{status="UP"} 0`, `statuspulse_check_response_duration_seconds_count 1`, `statuspulse_services 1`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
}
