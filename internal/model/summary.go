package model

type ServiceSummary struct {
	Service
	LatestCheck   *HealthCheck `json:"latest_check"`
	UptimePercent *float64     `json:"uptime_percent_24h"`
	SampleCount   int          `json:"sample_count_24h"`
}
