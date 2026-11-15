package config

import (
	"errors"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

// Worker database targets. Isolated is the default; main must be chosen explicitly.
const (
	TargetIsolated = "isolated"
	TargetMain     = "main"
)

// Analyzers. There is no default: the worker runs only with an explicit choice.
const (
	AnalyzerFake   = "fake"
	AnalyzerClaude = "claude"
	AnalyzerOpenAI = "openai"
)

// AIConfig holds the real analyzer settings, interpreted by the chosen provider.
// APIKey is a secret: it is passed to the client and must never be logged or
// included in an error.
type AIConfig struct {
	APIKey string
	Model  string
	Effort string
}

type WorkerConfig struct {
	DatabaseURL string
	Target      string
	Analyzer    string
	LogLevel    slog.Level
	Policy      worker.Policy
	Fake        worker.FakeAnalyzer
	AI          AIConfig
}

// Model IDs: letters, digits, '.', '_', ':', and '-' (fine-tuned IDs use ':').
// Real analyzer timing: defaults sized for a slow model call, and a floor below
// which attempts would be cancelled mid-call.
const (
	realAttemptTimeout    = 120 * time.Second
	realLease             = 180 * time.Second
	minRealAttemptTimeout = 60 * time.Second
)

var validModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

func LoadWorker() (WorkerConfig, error) {
	cfg := WorkerConfig{Target: value("WORKER_DATABASE_TARGET", TargetIsolated), Policy: worker.DefaultPolicy(),
		Analyzer: os.Getenv("WORKER_ANALYZER")}
	switch cfg.Analyzer {
	case AnalyzerFake, AnalyzerClaude, AnalyzerOpenAI:
	default:
		return cfg, errors.New("WORKER_ANALYZER must explicitly be fake, claude, or openai")
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
	realAnalyzer := cfg.Analyzer != AnalyzerFake
	if realAnalyzer {
		// A real analysis can take far longer than the fake one; a short deadline
		// cancels calls that may already be billed and retries them.
		cfg.Policy.AttemptTimeout, cfg.Policy.Lease = realAttemptTimeout, realLease
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
	if realAnalyzer {
		if cfg.Policy.AttemptTimeout < minRealAttemptTimeout {
			return cfg, errors.New("JOB_ATTEMPT_TIMEOUT must be at least 60s for the claude and openai analyzers")
		}
		return cfg, loadAI(&cfg.AI, cfg.Analyzer)
	}
	cfg.Fake.Outcome = value("WORKER_FAKE_OUTCOME", "success")
	cfg.Fake.Delay, err = time.ParseDuration(value("WORKER_FAKE_DELAY", "100ms"))
	if err != nil {
		return cfg, errors.New("WORKER_FAKE_DELAY must be a duration")
	}
	return cfg, cfg.Fake.Validate()
}

func loadAI(ai *AIConfig, analyzer string) error {
	ai.APIKey = os.Getenv("WORKER_AI_API_KEY")
	if ai.APIKey == "" || strings.ContainsFunc(ai.APIKey, unicode.IsSpace) {
		return errors.New("WORKER_AI_API_KEY is required for the " + analyzer + " analyzer and must not contain spaces")
	}
	ai.Model = os.Getenv("WORKER_AI_MODEL")
	if ai.Model == "" {
		// OpenAI's lineup changes often, so no OpenAI default is guessed.
		if analyzer == AnalyzerOpenAI {
			return errors.New("WORKER_AI_MODEL is required for the openai analyzer")
		}
		ai.Model = "claude-opus-5-5"
	}
	if !validModel.MatchString(ai.Model) {
		return errors.New("WORKER_AI_MODEL must be a model ID")
	}
	ai.Effort = value("WORKER_AI_EFFORT", "high")
	if !ValidEffort(ai.Effort) {
		return errors.New("WORKER_AI_EFFORT must be low, medium, high, xhigh, or max")
	}
	return nil
}

// ValidEffort is the single list of effort levels the worker accepts. Whether a
// given model supports a level is only known to the provider; an unsupported one
// releases the job and stops the worker instead of failing it.
func ValidEffort(effort string) bool {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
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
