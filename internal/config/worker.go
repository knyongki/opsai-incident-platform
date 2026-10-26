package config

import (
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

type WorkerConfig struct {
	DatabaseURL string
	LogLevel    slog.Level
	Policy      worker.Policy
	Fake        worker.FakeAnalyzer
}

func LoadWorker() (WorkerConfig, error) {
	cfg := WorkerConfig{DatabaseURL: os.Getenv("WORKER_DATABASE_URL"), Policy: worker.DefaultPolicy()}
	if os.Getenv("WORKER_ANALYZER") != "fake" {
		return cfg, errors.New("WORKER_ANALYZER must explicitly be fake for the isolated 4A demo")
	}
	if err := ValidateWorkerDatabaseURL(cfg.DatabaseURL); err != nil {
		return cfg, err
	}
	level := strings.ToUpper(value("LOG_LEVEL", "INFO"))
	if level != "DEBUG" && level != "INFO" && level != "WARN" && level != "ERROR" {
		return cfg, errors.New("LOG_LEVEL must be DEBUG, INFO, WARN, or ERROR")
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return cfg, errors.New("invalid LOG_LEVEL")
	}
	durations := []struct {
		key  string
		dest *time.Duration
	}{
		{"JOB_POLL_INTERVAL", &cfg.Policy.PollInterval},
		{"JOB_ATTEMPT_TIMEOUT", &cfg.Policy.AttemptTimeout},
		{"JOB_LEASE", &cfg.Policy.Lease},
		{"JOB_DATABASE_TIMEOUT", &cfg.Policy.DatabaseTimeout},
		{"JOB_RETRY_BASE", &cfg.Policy.RetryBase},
		{"JOB_RETRY_CAP", &cfg.Policy.RetryCap},
		{"WORKER_SHUTDOWN_TIMEOUT", &cfg.Policy.ShutdownTimeout},
	}
	for _, d := range durations {
		if raw := os.Getenv(d.key); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil {
				return cfg, errors.New(d.key + " must be a duration")
			}
			*d.dest = parsed
		}
	}
	var err error
	cfg.Policy.MaxAttempts, err = strconv.Atoi(value("JOB_MAX_ATTEMPTS", "3"))
	if err != nil {
		return cfg, errors.New("JOB_MAX_ATTEMPTS must be an integer")
	}
	if err := cfg.Policy.Validate(); err != nil {
		return cfg, err
	}
	cfg.Fake.Outcome = value("WORKER_FAKE_OUTCOME", "success")
	cfg.Fake.Delay, err = time.ParseDuration(value("WORKER_FAKE_DELAY", "100ms"))
	if err != nil {
		return cfg, errors.New("WORKER_FAKE_DELAY must be a duration")
	}
	return cfg, cfg.Fake.Validate()
}

// The separate URL and database suffix make consuming the regular queue an explicit setup error.
func ValidateWorkerDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		(u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") ||
		!strings.HasSuffix(u.Path, "_worker_test") || u.Fragment != "" {
		return errors.New("WORKER_DATABASE_URL must target a localhost database ending in _worker_test")
	}
	// Reject query overrides of the checked host/database, including service-file routing.
	parameters, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New("WORKER_DATABASE_URL query is malformed")
	}
	for key, values := range parameters {
		if len(values) != 1 {
			return errors.New("WORKER_DATABASE_URL contains duplicate connection parameters")
		}
		if key != "sslmode" && key != "connect_timeout" && key != "application_name" {
			return errors.New("WORKER_DATABASE_URL contains an unsupported connection parameter")
		}
	}
	if strings.Contains(u.Path[1:], "/") {
		return errors.New("WORKER_DATABASE_URL database name is invalid")
	}
	return nil
}
