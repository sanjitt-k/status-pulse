package model

import "time"

type CheckStatus string

const (
	StatusUp   CheckStatus = "UP"
	StatusDown CheckStatus = "DOWN"
)

// HealthCheck records one observation, not a guarantee of current availability.
type HealthCheck struct {
	ID             int64       `json:"id"`
	ServiceID      int64       `json:"service_id"`
	Status         CheckStatus `json:"status"`
	HTTPStatusCode *int        `json:"http_status_code"`
	ResponseTimeMS *int64      `json:"response_time_ms"`
	CheckedAt      time.Time   `json:"checked_at"`
	ErrorKind      string      `json:"error_kind,omitempty"`
	ErrorMessage   string      `json:"error_message,omitempty"`
}
