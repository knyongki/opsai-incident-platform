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

// Worker database targets. Isolated is the default; main must be chosen explicitly.
const (
	TargetIsolated = "isolated"
	TargetMain     = "main"
)

type WorkerConfig struct {
	DatabaseURL string
	Target      string
	LogLevel    slog.Level
	Policy      worker.Policy
	Fake        worker.FakeAnalyzer
}

func LoadWorker() (WorkerConfig, error) {
	cfg := WorkerConfig{Target: value("WORKER_DATABASE_TARGET", TargetIsolated), Policy: worker.DefaultPolicy()}
	if os.Getenv("WORKER_ANALYZER") != "fake" {
		return cfg, errors.New("WORKER_ANALYZER must explicitly be fake")
	}
	switch cfg.Target {
	case TargetIsolated:
		cfg.DatabaseURL = os.Getenv("WORKER_DATABASE_URL")
		if err := ValidateWorkerDatabaseURL(cfg.DatabaseURL); err != nil {
			return cfg, err
		}
	case TargetMain:
		// Never falls back to WORKER_DATABASE_URL: the target names exactly one database.
		cfg.DatabaseURL = os.Getenv("DATABASE_URL")
		if err := validateLocalDatabaseURL(cfg.DatabaseURL, "DATABASE_URL", false); err != nil {
			return cfg, err
		}
	default:
		return cfg, errors.New("WORKER_DATABASE_TARGET must be isolated or main")
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
	return validateLocalDatabaseURL(raw, "WORKER_DATABASE_URL", true)
}

// validateLocalDatabaseURL keeps the worker on localhost. Errors name the setting,
// never its value, because the URL may contain credentials.
func validateLocalDatabaseURL(raw, key string, testSuffix bool) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		(u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") ||
		len(u.Path) < 2 || (testSuffix && !strings.HasSuffix(u.Path, "_worker_test")) || u.Fragment != "" {
		if testSuffix {
			return errors.New(key + " must target a localhost database ending in _worker_test")
		}
		return errors.New(key + " must target a localhost database")
	}
	// Reject query overrides of the checked host/database, including service-file routing.
	parameters, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return errors.New(key + " query is malformed")
	}
	for name, values := range parameters {
		if len(values) != 1 {
			return errors.New(key + " contains duplicate connection parameters")
		}
		if name != "sslmode" && name != "connect_timeout" && name != "application_name" {
			return errors.New(key + " contains an unsupported connection parameter")
		}
	}
	if strings.Contains(u.Path[1:], "/") {
		return errors.New(key + " database name is invalid")
	}
	return nil
}
