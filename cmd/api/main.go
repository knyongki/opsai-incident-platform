package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/config"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database"
	"github.com/knyongki/opsai-incident-platform/internal/platform/logging"
	httpapi "github.com/knyongki/opsai-incident-platform/internal/transport/http"
)

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	if err := run(logger); err != nil {
		logger.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(bootstrapLogger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.New(os.Stderr, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// serve waits for HTTP shutdown before this deferred cleanup runs.
	defer pool.Close()
	server := httpapi.NewServer(cfg.HTTPAddress, logger, pool)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		// Avoid including raw configuration values in startup errors.
		bootstrapLogger.Error("HTTP listener could not start")
		return errors.New("cannot listen on HTTP_ADDRESS; check host, port, and availability")
	}
	logger.Info("API listening")
	return serve(ctx, server, listener, logger)
}

func serve(ctx context.Context, server *http.Server, listener net.Listener, logger *slog.Logger) error {
	result := make(chan error, 1)
	go func() {
		result <- server.Serve(listener)
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		logger.Info("API shutting down")
	}
	// The signal context is already canceled; draining needs a fresh deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	serveErr := <-result
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if err := errors.Join(shutdownErr, serveErr); err != nil {
		return err
	}
	logger.Info("API stopped cleanly")
	return nil
}
