package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
	"statuspulse/internal/web"
)

func TestChecker(t *testing.T) {
	for _, code := range []int{200, 204, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer endpoint.Close()
			checker := NewChecker(time.Second)
			defer checker.Close()
			result, err := checker.Check(context.Background(), model.Service{ID: 1, URL: endpoint.URL})
			if err != nil {
				t.Fatal(err)
			}
			want := model.StatusUp
			if code >= 300 {
				want = model.StatusDown
			}
			if result.Status != want || result.HTTPStatusCode == nil || *result.HTTPStatusCode != code || result.ResponseTimeMS == nil || result.CheckedAt.IsZero() {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestTimeoutAndConnectionFailure(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	checker := NewChecker(50 * time.Millisecond)
	defer checker.Close()
	result, err := checker.Check(context.Background(), model.Service{URL: endpoint.URL})
	endpoint.Close()
	if err != nil || result.Status != model.StatusDown || result.ErrorKind != "timeout" || result.HTTPStatusCode != nil || result.ResponseTimeMS != nil {
		t.Fatalf("timeout: %+v, %v", result, err)
	}
	result, err = checker.Check(context.Background(), model.Service{URL: endpoint.URL})
	if err != nil || result.Status != model.StatusDown || result.HTTPStatusCode != nil || result.ErrorKind == "" {
		t.Fatalf("connection failure: %+v, %v", result, err)
	}
}

func TestRedirects(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/loop" {
			http.Redirect(w, r, "/loop", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/ok", http.StatusFound)
	}))
	defer endpoint.Close()
	checker := NewChecker(time.Second)
	defer checker.Close()
	result, err := checker.Check(context.Background(), model.Service{URL: endpoint.URL})
	if err != nil || result.Status != model.StatusUp || *result.HTTPStatusCode != 204 {
		t.Fatalf("redirect: %+v, %v", result, err)
	}
	result, err = checker.Check(context.Background(), model.Service{URL: endpoint.URL + "/loop"})
	if err != nil || result.Status != model.StatusDown || result.ErrorKind == "" {
		t.Fatalf("loop: %+v, %v", result, err)
	}
}

func TestSchedulerCancellationAndDeletion(t *testing.T) {
	for _, deleteService := range []bool{false, true} {
		started := make(chan struct{})
		release := make(chan struct{})
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		s := store.NewMemoryStore()
		service, _ := s.Create(context.Background(), model.Service{URL: endpoint.URL})
		checker := NewChecker(time.Second)
		scheduler := NewScheduler(s, checker, time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); scheduler.Run(ctx) }()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("check never started")
		}
		if deleteService {
			if err := s.Delete(context.Background(), service.ID); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		close(release)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("scheduler failed to stop")
		}
		checks, err := s.ListChecks(context.Background(), service.ID, 50, 0)
		if len(checks) != 0 || (!deleteService && err != nil) {
			t.Fatalf("unexpected shutdown results: %v, %v", checks, err)
		}
		checker.Close()
		endpoint.Close()
	}
}

func TestAPIRegistrationMonitoringHistoryAndDeletion(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer endpoint.Close()
	s := store.NewMemoryStore()
	api := web.NewHandler(s, time.Minute)
	created := httptest.NewRecorder()
	api.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/services", strings.NewReader(`{"name":"Local","url":"`+endpoint.URL+`"}`)))
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	checker := NewChecker(time.Second)
	defer checker.Close()
	scheduler := NewScheduler(s, checker, time.Minute)
	scheduler.runCycle(context.Background())
	scheduler.runCycle(context.Background())
	history := httptest.NewRecorder()
	api.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/api/services/1/checks?limit=1&offset=1", nil))
	var checks []model.HealthCheck
	if err := json.Unmarshal(history.Body.Bytes(), &checks); err != nil {
		t.Fatal(err)
	}
	if history.Code != 200 || len(checks) != 1 || checks[0].ID != 1 || checks[0].Status != model.StatusDown || *checks[0].HTTPStatusCode != 503 {
		t.Fatalf("unexpected history: %s", history.Body)
	}
	deleted := httptest.NewRecorder()
	api.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/services/1", nil))
	history = httptest.NewRecorder()
	api.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/api/services/1/checks", nil))
	if deleted.Code != 204 || history.Code != 404 {
		t.Fatal("delete did not remove service history")
	}
}
