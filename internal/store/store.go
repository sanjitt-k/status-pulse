package store

import (
	"context"
	"errors"
	"time"

	"statuspulse/internal/model"
)

// ErrNotFound is returned when a requested service does not exist.
var ErrNotFound = errors.New("service not found")

// ServiceStore describes the storage operations needed by the HTTP handlers.
type ServiceStore interface {
	Create(context.Context, model.Service) (model.Service, error)
	List(context.Context) ([]model.Service, error)
	Get(context.Context, int64) (model.Service, error)
	Delete(context.Context, int64) error
	SaveCheck(context.Context, model.HealthCheck) error
	ListChecks(context.Context, int64, int, int) ([]model.HealthCheck, error)
	Summary(context.Context, int64, time.Time) (model.ServiceSummary, error)
}
