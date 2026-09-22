package config

import (
	"fmt"
	"os"
	"time"
)

const defaultListenAddress = ":8080"

// Config contains the settings needed to start StatusPulse.
type Config struct {
	ListenAddress  string
	CheckInterval  time.Duration
	RequestTimeout time.Duration
}

// Load reads configuration from the environment and applies defaults.
func Load() (Config, error) {
	listenAddress := os.Getenv("STATUSPULSE_ADDR")
	if listenAddress == "" {
		listenAddress = defaultListenAddress
	}

	interval, err := duration("STATUSPULSE_CHECK_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	timeout, err := duration("STATUSPULSE_REQUEST_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	return Config{ListenAddress: listenAddress, CheckInterval: interval, RequestTimeout: timeout}, nil
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 5s or 1m", name)
	}
	return parsed, nil
}
