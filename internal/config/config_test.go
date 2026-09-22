package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	for _, name := range []string{"STATUSPULSE_ADDR", "STATUSPULSE_DB_PATH", "STATUSPULSE_CHECK_INTERVAL", "STATUSPULSE_REQUEST_TIMEOUT"} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != ":8080" || cfg.DatabasePath != "statuspulse.db" || cfg.CheckInterval != time.Minute || cfg.RequestTimeout != 5*time.Second {
		t.Fatalf("defaults: %+v", cfg)
	}
	t.Setenv("STATUSPULSE_ADDR", "127.0.0.1:9000")
	t.Setenv("STATUSPULSE_DB_PATH", "custom.db")
	t.Setenv("STATUSPULSE_CHECK_INTERVAL", "2m")
	t.Setenv("STATUSPULSE_REQUEST_TIMEOUT", "500ms")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:9000" || cfg.DatabasePath != "custom.db" || cfg.CheckInterval != 2*time.Minute || cfg.RequestTimeout != 500*time.Millisecond {
		t.Fatalf("overrides: %+v", cfg)
	}
}

func TestInvalidDurations(t *testing.T) {
	for _, name := range []string{"STATUSPULSE_CHECK_INTERVAL", "STATUSPULSE_REQUEST_TIMEOUT"} {
		for _, value := range []string{"0s", "-1s", "5", "nonsense", "999999999999999999h"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				t.Setenv("STATUSPULSE_CHECK_INTERVAL", "1m")
				t.Setenv("STATUSPULSE_REQUEST_TIMEOUT", "5s")
				t.Setenv(name, value)
				if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("expected named configuration error, got %v", err)
				}
			})
		}
	}
}
