package model

import "time"

// Service is an HTTP endpoint registered with StatusPulse.
type Service struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}
