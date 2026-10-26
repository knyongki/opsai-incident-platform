package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/config"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database"
	"github.com/knyongki/opsai-incident-platform/internal/platform/logging"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

func main() {
	checkConfig := flag.Bool("check-config", false, "validate worker configuration without connecting or processing")
	flag.Parse()
	logger := logging.New(os.Stderr, slog.LevelInfo)
	if err := run(*checkConfig); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
func run(checkConfig bool) error {
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	if checkConfig {
		return nil
	}
	logger := logging.New(os.Stderr, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	repo, err := database.NewJobRepository(pool, cfg.Policy)
	if err != nil {
		pool.Close()
		return err
	}
	w, err := worker.New(repo, cfg.Fake, cfg.Policy, logger)
	if err != nil {
		pool.Close()
		return err
	}
	logger.Info("fake worker started", "concurrency", 1)
	err = serve(ctx, w.Run, cfg.Policy.ShutdownTimeout)
	// On a shutdown timeout main exits the process; pool.Close could otherwise block indefinitely.
	if err != nil {
		return err
	}
	pool.Close()
	logger.Info("worker stopped cleanly")
	return nil
}
func serve(ctx context.Context, run func(context.Context), timeout time.Duration) error {
	done := make(chan struct{})
	go func() { run(ctx); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return errors.New("worker shutdown deadline exceeded; unfinished jobs remain leased for recovery")
	}
}
