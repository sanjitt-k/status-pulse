package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteReadiness(t *testing.T) {
	s, err := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "ready.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Ready(ctx); err == nil {
		t.Fatal("canceled readiness succeeded")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(context.Background()); err == nil {
		t.Fatal("closed database reported ready")
	}
}
