package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthAndMetrics(t *testing.T) {
	for _, failed := range []bool{false, true} {
		m := New()
		m.Checks.WithLabelValues("UP").Inc()
		h := m.Handler(http.NotFoundHandler(), func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("readiness needs a deadline")
			}
			if failed {
				return errors.New("private database path")
			}
			return nil
		})
		for _, path := range []string{"/livez", "/readyz", "/metrics"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			want := 200
			if failed && path == "/readyz" {
				want = 503
			}
			if w.Code != want {
				t.Errorf("%s: got %d want %d", path, w.Code, want)
			}
			if strings.Contains(w.Body.String(), "private database path") {
				t.Error("readiness leaked internal error")
			}
			if path == "/metrics" && !strings.Contains(w.Body.String(), `statuspulse_checks_total{status="UP"} 1`) {
				t.Error("missing recorded check")
			}
		}
	}
}

func TestRegistryIsolation(t *testing.T) {
	first, second := New(), New()
	first.Checks.WithLabelValues("DOWN").Inc()
	w := httptest.NewRecorder()
	second.Handler(http.NotFoundHandler(), func(context.Context) error { return nil }).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), `statuspulse_checks_total{status="DOWN"} 0`) {
		t.Fatal("registries share state")
	}
}
