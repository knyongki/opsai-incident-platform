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

	"github.com/knyongki/opsai-incident-platform/internal/analyzer/claude"
	"github.com/knyongki/opsai-incident-platform/internal/analyzer/openai"
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
	analyzer, err := newAnalyzer(cfg)
	if err != nil {
		return err
	}
	if c, ok := analyzer.(providerChecker); ok {
		// Verify the key and model before claiming any job; a rejected key must not
		// consume queued work.
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := c.Check(checkCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return checkFailure(cfg.Analyzer, err)
		}
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	repo, err := database.NewJobRepository(pool, cfg.Policy)
	if err != nil {
		pool.Close()
		return err
	}
	w, err := worker.New(repo, analyzer, cfg.Policy, logger)
	if err != nil {
		pool.Close()
		return err
	}
	if cfg.Target == config.TargetMain {
		logger.Warn("worker is writing stored analysis results to the main local database",
			"database_target", cfg.Target, "analyzer", cfg.Analyzer)
	}
	if cfg.Analyzer != config.AnalyzerFake {
		logger.Warn("analyzer sends incident and device details to its provider's API; each analysis is billed",
			"analyzer", cfg.Analyzer, "model", cfg.AI.Model, "effort", cfg.AI.Effort)
	}
	logger.Info("worker started", "analyzer", cfg.Analyzer, "database_target", cfg.Target, "concurrency", 1)
	err = serve(ctx, w.Run, cfg.Policy.ShutdownTimeout)
	// On a shutdown timeout main exits the process; pool.Close could otherwise block indefinitely.
	if err != nil {
		return err
	}
	pool.Close()
	logger.Info("worker stopped cleanly")
	return nil
}

// providerChecker is implemented by real analyzers, which verify their key and
// model at startup.
type providerChecker interface {
	Check(context.Context) error
}

// newAnalyzer is the only place that knows the providers; adding one means one
// case here and one adapter package.
func newAnalyzer(cfg config.WorkerConfig) (worker.Analyzer, error) {
	switch cfg.Analyzer {
	case config.AnalyzerFake:
		return cfg.Fake, nil
	case config.AnalyzerClaude:
		return claude.New(claude.Config{APIKey: cfg.AI.APIKey, Model: cfg.AI.Model, Effort: cfg.AI.Effort})
	case config.AnalyzerOpenAI:
		return openai.New(openai.Config{APIKey: cfg.AI.APIKey, Model: cfg.AI.Model, Effort: cfg.AI.Effort})
	}
	// config.LoadWorker already rejects unknown analyzers.
	return nil, errors.New("analyzer has no adapter")
}

// checkFailure turns a classified startup-check error into an operator message
// that names the actual cause. Ctrl+C during the check is a clean exit.
func checkFailure(analyzer string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, worker.ErrProviderConfig):
		return errors.New(analyzer + " provider rejected the configuration; verify WORKER_AI_API_KEY, WORKER_AI_MODEL, and the account")
	default:
		return errors.New(analyzer + " provider is unreachable or unavailable; check the network and try again")
	}
}

func serve(ctx context.Context, run func(context.Context) error, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errors.New("worker shutdown deadline exceeded; unfinished jobs remain leased for recovery")
	}
}
