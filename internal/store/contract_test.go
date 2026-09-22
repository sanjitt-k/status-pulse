package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"statuspulse/internal/model"
)

func TestStoreConcurrentOperations(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var s ServiceStore = NewMemoryStore()
			if backend == "sqlite" {
				db, err := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "concurrent.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				s = db
			}
			ctx := context.Background()
			var wg sync.WaitGroup
			errors := make(chan error, 24)
			for i := 0; i < 24; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					service, err := s.Create(ctx, model.Service{Name: fmt.Sprintf("Service %d", i), URL: "https://example.com"})
					if err != nil {
						errors <- err
						return
					}
					if err := s.SaveCheck(ctx, model.HealthCheck{ServiceID: service.ID, Status: model.StatusUp, CheckedAt: time.Now()}); err != nil {
						errors <- err
						return
					}
					if _, err := s.List(ctx); err != nil {
						errors <- err
						return
					}
					if _, err := s.Summary(ctx, service.ID, time.Now()); err != nil {
						errors <- err
						return
					}
					if i%2 == 0 {
						if err := s.Delete(ctx, service.ID); err != nil {
							errors <- err
						}
					}
				}(i)
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				t.Error(err)
			}
			services, err := s.List(ctx)
			if err != nil || len(services) != 12 {
				t.Fatalf("remaining services: %d %v", len(services), err)
			}
			for i, service := range services {
				if i > 0 && services[i-1].ID >= service.ID {
					t.Fatal("IDs not unique and sorted")
				}
				checks, err := s.ListChecks(ctx, service.ID, 50, 0)
				if err != nil || len(checks) != 1 {
					t.Fatalf("lost check: %+v %v", checks, err)
				}
			}
		})
	}
}

func TestMemoryCheckCopiesAndRetention(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	service, err := s.Create(ctx, model.Service{Name: "Example", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	latency := int64(5)
	if err := s.SaveCheck(ctx, model.HealthCheck{ServiceID: service.ID, Status: model.StatusUp, HTTPStatusCode: &code, ResponseTimeMS: &latency, CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	code = 500
	latency = 999
	checks, err := s.ListChecks(ctx, service.ID, 1, 0)
	if err != nil || len(checks) != 1 || *checks[0].HTTPStatusCode != 200 || *checks[0].ResponseTimeMS != 5 {
		t.Fatalf("stored pointers changed: %+v %v", checks, err)
	}
	*checks[0].HTTPStatusCode = 404
	again, err := s.ListChecks(ctx, service.ID, 1, 0)
	if err != nil || *again[0].HTTPStatusCode != 200 {
		t.Fatal("read result aliases storage")
	}
	for i := 0; i < 1005; i++ {
		if err := s.SaveCheck(ctx, model.HealthCheck{ServiceID: service.ID, Status: model.StatusDown, CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListChecks(ctx, service.ID, 2000, 0)
	if err != nil || len(all) != 1000 || all[0].ID != 1006 || all[999].ID != 7 {
		t.Fatalf("retention incorrect: %d %v", len(all), err)
	}
}
