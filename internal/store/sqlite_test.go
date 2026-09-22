package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"statuspulse/internal/model"
)

func TestSQLitePersistenceAndIntegrity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test database.db")
	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	service, err := s.Create(ctx, model.Service{Name: "Example", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	empty, err := s.Summary(ctx, service.ID, now)
	if err != nil || empty.LatestCheck != nil || empty.UptimePercent != nil || empty.SampleCount != 0 {
		t.Fatalf("empty summary: %+v %v", empty, err)
	}
	code, latency := 200, int64(42)
	for _, check := range []model.HealthCheck{
		{ServiceID: service.ID, Status: model.StatusDown, CheckedAt: now.Add(-25 * time.Hour)},
		{ServiceID: service.ID, Status: model.StatusUp, HTTPStatusCode: &code, ResponseTimeMS: &latency, CheckedAt: now},
		{ServiceID: service.ID, Status: model.StatusDown, ErrorKind: "timeout", ErrorMessage: "deadline exceeded", CheckedAt: now},
	} {
		if err := s.SaveCheck(ctx, check); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Get(ctx, service.ID)
	if err != nil || reloaded != service {
		t.Fatalf("service not persisted: %+v %v", reloaded, err)
	}
	checks, err := s.ListChecks(ctx, service.ID, 50, 0)
	if err != nil || len(checks) != 3 {
		t.Fatalf("history: %+v %v", checks, err)
	}
	if checks[0].ID != 3 || checks[1].ID != 2 || checks[0].HTTPStatusCode != nil || checks[0].ResponseTimeMS != nil || checks[0].ErrorKind != "timeout" || *checks[1].ResponseTimeMS != 42 {
		t.Fatalf("history order or nulls incorrect: %+v", checks)
	}
	page, err := s.ListChecks(ctx, service.ID, 1, 1)
	if err != nil || len(page) != 1 || page[0].ID != 2 {
		t.Fatalf("pagination: %+v %v", page, err)
	}
	summary, err := s.Summary(ctx, service.ID, now)
	if err != nil || summary.SampleCount != 2 || summary.UptimePercent == nil || *summary.UptimePercent != 50 || summary.LatestCheck.ID != 3 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("migration reapplied: %d %v", versions, err)
	}
	// Force a replacement connection and verify the per-connection foreign key pragma.
	s.db.SetMaxIdleConns(0)
	if err := s.Delete(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM health_checks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade failed: %d %v", count, err)
	}
	if err := s.SaveCheck(ctx, model.HealthCheck{ServiceID: service.ID, Status: model.StatusUp, CheckedAt: now}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted service accepted check: %v", err)
	}
	if _, err := s.ListChecks(ctx, service.ID, 50, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing history error: %v", err)
	}
	other, err := s.Create(ctx, model.Service{Name: "Next", URL: "https://example.com"})
	if err != nil || other.ID <= service.ID {
		t.Fatalf("ID reused: %+v %v", other, err)
	}
}

func TestSQLiteUptimeBoundaries(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "uptime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service, err := s.Create(ctx, model.Service{Name: "Test", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, status := range []model.CheckStatus{model.StatusUp, model.StatusDown} {
		if _, err := s.db.Exec(`DELETE FROM health_checks`); err != nil {
			t.Fatal(err)
		}
		for _, timestamp := range []time.Time{now.Add(-24 * time.Hour), now, now.Add(time.Hour), now.Add(-25 * time.Hour)} {
			if err := s.SaveCheck(ctx, model.HealthCheck{ServiceID: service.ID, Status: status, CheckedAt: timestamp}); err != nil {
				t.Fatal(err)
			}
		}
		result, err := s.Summary(ctx, service.ID, now)
		want := float64(0)
		if status == model.StatusUp {
			want = 100
		}
		if err != nil || result.SampleCount != 2 || result.UptimePercent == nil || *result.UptimePercent != want {
			t.Fatalf("window: %+v %v", result, err)
		}
	}
}

func TestSQLiteOpenFailure(t *testing.T) {
	if s, err := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "missing", "db.sqlite")); err == nil {
		s.Close()
		t.Fatal("expected invalid parent directory to fail")
	}
}
