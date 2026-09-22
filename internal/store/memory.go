package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"statuspulse/internal/model"
)

// MemoryStore keeps services in memory. Its data is lost when the app stops.
type MemoryStore struct {
	mu          sync.RWMutex
	services    map[int64]model.Service
	nextID      int64
	checks      map[int64][]model.HealthCheck
	nextCheckID int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		services:    make(map[int64]model.Service),
		nextID:      1,
		checks:      make(map[int64][]model.HealthCheck),
		nextCheckID: 1,
	}
}

func (s *MemoryStore) Create(ctx context.Context, service model.Service) (model.Service, error) {
	if err := ctx.Err(); err != nil {
		return model.Service{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	service.ID = s.nextID
	service.CreatedAt = time.Now().UTC()
	s.services[service.ID] = service
	s.nextID++

	return service, nil
}

func (s *MemoryStore) List(ctx context.Context) ([]model.Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	services := make([]model.Service, 0, len(s.services))
	for _, service := range s.services {
		services = append(services, service)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].ID < services[j].ID
	})

	return services, nil
}

func (s *MemoryStore) Get(ctx context.Context, id int64) (model.Service, error) {
	if err := ctx.Err(); err != nil {
		return model.Service{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	service, ok := s.services[id]
	if !ok {
		return model.Service{}, ErrNotFound
	}

	return service, nil
}

func (s *MemoryStore) Delete(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.services[id]; !ok {
		return ErrNotFound
	}

	delete(s.services, id)
	delete(s.checks, id)
	return nil
}

// Keep memory bounded until persistent history arrives in Phase 4.
const maxChecksPerService = 1000

func (s *MemoryStore) SaveCheck(ctx context.Context, check model.HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := s.services[check.ServiceID]; !ok {
		return ErrNotFound
	}
	check.ID = s.nextCheckID
	s.nextCheckID++
	history := append(s.checks[check.ServiceID], cloneCheck(check))
	if len(history) > maxChecksPerService {
		copy(history, history[len(history)-maxChecksPerService:])
		history = history[:maxChecksPerService]
	}
	s.checks[check.ServiceID] = history
	return nil
}

// ListChecks returns newest saved results first, with an empty array before
// the first observation. Copies prevent callers from mutating stored pointers.
func (s *MemoryStore) ListChecks(ctx context.Context, id int64, limit, offset int) ([]model.HealthCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := s.services[id]; !ok {
		return nil, ErrNotFound
	}
	results := make([]model.HealthCheck, 0)
	if limit <= 0 || offset < 0 {
		return results, nil
	}
	history := s.checks[id]
	if offset >= len(history) {
		return results, nil
	}
	for i := len(history) - 1 - offset; i >= 0 && len(results) < limit; i-- {
		results = append(results, cloneCheck(history[i]))
	}
	return results, nil
}

func cloneCheck(check model.HealthCheck) model.HealthCheck {
	if check.HTTPStatusCode != nil {
		value := *check.HTTPStatusCode
		check.HTTPStatusCode = &value
	}
	if check.ResponseTimeMS != nil {
		value := *check.ResponseTimeMS
		check.ResponseTimeMS = &value
	}
	return check
}

func (s *MemoryStore) Summary(ctx context.Context, id int64, now time.Time) (model.ServiceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return model.ServiceSummary{}, err
	}
	service, ok := s.services[id]
	if !ok {
		return model.ServiceSummary{}, ErrNotFound
	}
	result := model.ServiceSummary{Service: service}
	up := 0
	for _, check := range s.checks[id] {
		if result.LatestCheck == nil || check.CheckedAt.After(result.LatestCheck.CheckedAt) || (check.CheckedAt.Equal(result.LatestCheck.CheckedAt) && check.ID > result.LatestCheck.ID) {
			copy := cloneCheck(check)
			result.LatestCheck = &copy
		}
		if check.CheckedAt.Before(now.Add(-24*time.Hour)) || check.CheckedAt.After(now) {
			continue
		}
		result.SampleCount++
		if check.Status == model.StatusUp {
			up++
		}
	}
	if result.SampleCount > 0 {
		percent := float64(up) * 100 / float64(result.SampleCount)
		result.UptimePercent = &percent
	}
	return result, nil
}
