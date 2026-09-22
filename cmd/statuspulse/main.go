package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"statuspulse/internal/config"
	"statuspulse/internal/monitor"
	"statuspulse/internal/store"
	"statuspulse/internal/web"
)

const shutdownTimeout = 5 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	serviceStore, err := store.OpenSQLite(startupContext, cfg.DatabasePath)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer serviceStore.Close()
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerContext, cancelWorker := context.WithCancel(signalContext)
	checker := monitor.NewChecker(cfg.RequestTimeout)
	defer checker.Close()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		monitor.NewScheduler(serviceStore, checker, cfg.CheckInterval).Run(workerContext)
	}()
	defer func() { cancelWorker(); <-workerDone }()

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           web.NewHandler(serviceStore),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("starting StatusPulse", "address", cfg.ListenAddress)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signalContext.Done():
		slog.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}

	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}

	slog.Info("StatusPulse stopped")
	return nil
}
