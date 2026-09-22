package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"statuspulse/internal/model"
	"statuspulse/internal/store"
	"statuspulse/internal/web"
)

// Exercise real HTTP on both sides, a real checker, and a file-backed database.
// Running a cycle explicitly avoids waiting for a wall-clock monitoring interval.
func TestSQLiteMonitoringEndToEnd(t *testing.T) {
	ctx := context.Background()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer endpoint.Close()
	path := filepath.Join(t.TempDir(), "integration.db")
	db, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	api := httptest.NewServer(web.NewHandler(db, time.Minute))
	defer func() { api.Close() }()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	body, err := json.Marshal(map[string]string{"name": "Integration service", "url": endpoint.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Post(api.URL+"/api/services", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var service model.Service
	err = json.NewDecoder(response.Body).Decode(&service)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 || service.ID != 1 {
		t.Fatalf("registration: %+v %v status=%d", service, err, response.StatusCode)
	}
	checker := NewChecker(time.Second)
	defer checker.Close()
	NewScheduler(db, checker, time.Minute).runCycle(ctx)
	api.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	api = httptest.NewServer(web.NewHandler(db, time.Minute))
	response, err = client.Get(api.URL + "/api/services/1/checks")
	if err != nil {
		t.Fatal(err)
	}
	var checks []model.HealthCheck
	err = json.NewDecoder(response.Body).Decode(&checks)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || len(checks) != 1 {
		t.Fatalf("history after restart: %+v %v", checks, err)
	}
	if checks[0].Status != model.StatusUp || checks[0].HTTPStatusCode == nil || *checks[0].HTTPStatusCode != 204 || checks[0].ResponseTimeMS == nil {
		t.Fatalf("persisted result: %+v", checks[0])
	}
	response, err = client.Get(api.URL + "/api/services/1/summary")
	if err != nil {
		t.Fatal(err)
	}
	var summary model.ServiceSummary
	err = json.NewDecoder(response.Body).Decode(&summary)
	response.Body.Close()
	if err != nil || summary.Name != "Integration service" || summary.SampleCount != 1 || summary.UptimePercent == nil || *summary.UptimePercent != 100 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
}
