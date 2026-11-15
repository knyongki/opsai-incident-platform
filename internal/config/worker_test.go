package config

import (
	"strings"
	"testing"
	"time"
)

func workerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"WORKER_ANALYZER", "WORKER_DATABASE_URL", "WORKER_DATABASE_TARGET", "DATABASE_URL",
		"WORKER_FAKE_OUTCOME", "WORKER_FAKE_DELAY",
		"JOB_POLL_INTERVAL", "JOB_ATTEMPT_TIMEOUT", "JOB_LEASE", "JOB_DATABASE_TIMEOUT", "JOB_RETRY_BASE",
		"JOB_RETRY_CAP", "JOB_MAX_ATTEMPTS", "WORKER_SHUTDOWN_TIMEOUT", "LOG_LEVEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("WORKER_ANALYZER", "fake")
	t.Setenv("WORKER_DATABASE_URL", "postgres://example:example@127.0.0.1:5432/opsai_worker_test?sslmode=disable")
}
func TestLoadWorker(t *testing.T) {
	workerEnv(t)
	cfg, err := LoadWorker()
	if err != nil || cfg.Policy.MaxAttempts != 3 || cfg.Policy.Lease != time.Minute || cfg.Fake.Outcome != "success" ||
		cfg.Target != TargetIsolated || !strings.HasSuffix(cfg.DatabaseURL, "_worker_test?sslmode=disable") {
		t.Fatal("worker defaults failed")
	}
	t.Setenv("JOB_MAX_ATTEMPTS", "4")
	t.Setenv("WORKER_FAKE_OUTCOME", "transient-once")
	cfg, err = LoadWorker()
	if err != nil || cfg.Policy.MaxAttempts != 4 || cfg.Fake.Outcome != "transient-once" {
		t.Fatal("worker overrides failed")
	}
	t.Setenv("WORKER_FAKE_OUTCOME", "invalid-output")
	cfg, err = LoadWorker()
	if err != nil || cfg.Fake.Outcome != "invalid-output" {
		t.Fatal("invalid output test mode rejected")
	}
}
func TestRejectUnsafeWorkerConfiguration(t *testing.T) {
	for _, tc := range []struct{ key, val string }{
		{"WORKER_ANALYZER", ""}, {"WORKER_ANALYZER", "real"},
		{"WORKER_DATABASE_URL", "postgres://example:secret@127.0.0.1/opsai"},
		{"WORKER_DATABASE_URL", "postgres://example:secret@remote/opsai_worker_test"},
		{"WORKER_DATABASE_URL", "postgres://example:secret@localhost/opsai_worker_test?host=remote"},
		{"WORKER_DATABASE_URL", "postgres://example:secret@localhost/opsai_worker_test?dbname=opsai"},
		{"WORKER_DATABASE_URL", "secret"},
		{"WORKER_DATABASE_URL", "postgres://localhost/opsai_worker_test?sslmode=disable&sslmode=require"},
		{"WORKER_DATABASE_URL", "postgres://localhost/opsai_worker_test?sslmode=%zz"},
		{"JOB_MAX_ATTEMPTS", "0"}, {"JOB_MAX_ATTEMPTS", "secret"},
		{"JOB_LEASE", "1s"}, {"JOB_RETRY_CAP", "1ms"},
		{"JOB_POLL_INTERVAL", "secret"}, {"WORKER_FAKE_DELAY", "-1s"},
		{"WORKER_FAKE_OUTCOME", "secret"}, {"LOG_LEVEL", "secret"}, {"LOG_LEVEL", "INFO+2"},
		{"WORKER_DATABASE_TARGET", "secret"}, {"WORKER_DATABASE_TARGET", "MAIN"},
	} {
		t.Run(tc.key+"/"+tc.val, func(t *testing.T) {
			workerEnv(t)
			t.Setenv(tc.key, tc.val)
			_, err := LoadWorker()
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid setting accepted or exposed")
			}
		})
	}
}

func TestWorkerDatabaseTarget(t *testing.T) {
	const main = "postgres://example:example@localhost:5432/opsai?sslmode=disable"
	t.Run("isolated explicit", func(t *testing.T) {
		workerEnv(t)
		t.Setenv("WORKER_DATABASE_TARGET", "isolated")
		t.Setenv("DATABASE_URL", main)
		cfg, err := LoadWorker()
		if err != nil || cfg.Target != TargetIsolated || cfg.DatabaseURL == main {
			t.Fatal("isolated target must use only WORKER_DATABASE_URL")
		}
	})
	t.Run("main uses DATABASE_URL", func(t *testing.T) {
		workerEnv(t)
		t.Setenv("WORKER_DATABASE_TARGET", "main")
		t.Setenv("DATABASE_URL", main)
		cfg, err := LoadWorker()
		if err != nil || cfg.Target != TargetMain || cfg.DatabaseURL != main {
			t.Fatal("main target did not select DATABASE_URL")
		}
	})
	for _, raw := range []string{
		"",
		"postgres://example:secret@remote.example/opsai",
		"postgres://example:secret@localhost/opsai?host=remote",
		"postgres://example:secret@localhost/",
		"mysql://example:secret@localhost/opsai",
		"secret",
	} {
		t.Run("main rejects "+raw, func(t *testing.T) {
			workerEnv(t)
			t.Setenv("WORKER_DATABASE_TARGET", "main")
			t.Setenv("DATABASE_URL", raw)
			_, err := LoadWorker()
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe main database accepted or exposed")
			}
		})
	}
	t.Run("main never falls back to the worker URL", func(t *testing.T) {
		workerEnv(t)
		t.Setenv("WORKER_DATABASE_TARGET", "main")
		if _, err := LoadWorker(); err == nil {
			t.Fatal("main target used WORKER_DATABASE_URL")
		}
	})
}

func TestClaudeAnalyzerConfiguration(t *testing.T) {
	const key = "example-key-not-real-secret"
	claudeEnv := func(t *testing.T) {
		workerEnv(t)
		for _, k := range []string{"WORKER_AI_API_KEY", "WORKER_AI_MODEL", "WORKER_AI_EFFORT"} {
			t.Setenv(k, "")
		}
		t.Setenv("WORKER_ANALYZER", "claude")
		t.Setenv("WORKER_AI_API_KEY", key)
	}
	t.Run("defaults", func(t *testing.T) {
		claudeEnv(t)
		cfg, err := LoadWorker()
		if err != nil || cfg.Analyzer != AnalyzerClaude || cfg.AI.APIKey != key ||
			cfg.AI.Model != "claude-opus-5-5" || cfg.AI.Effort != "high" {
			t.Fatalf("claude defaults failed: %v", err)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		claudeEnv(t)
		t.Setenv("WORKER_AI_MODEL", "claude-sonnet-5-5")
		t.Setenv("WORKER_AI_EFFORT", "medium")
		cfg, err := LoadWorker()
		if err != nil || cfg.AI.Model != "claude-sonnet-5-5" || cfg.AI.Effort != "medium" {
			t.Fatal("claude overrides failed")
		}
	})
	t.Run("fake keeps working without AI settings", func(t *testing.T) {
		workerEnv(t)
		t.Setenv("WORKER_AI_API_KEY", "")
		cfg, err := LoadWorker()
		if err != nil || cfg.Analyzer != AnalyzerFake || cfg.AI.APIKey != "" {
			t.Fatal("fake analyzer config changed")
		}
	})
	for _, tc := range []struct{ name, key, val string }{
		{"missing key", "WORKER_AI_API_KEY", ""},
		{"blank key", "WORKER_AI_API_KEY", "   "},
		{"key with whitespace", "WORKER_AI_API_KEY", "example secret"},
		{"bad effort", "WORKER_AI_EFFORT", "secret"},
		{"bad model", "WORKER_AI_MODEL", "secret model"},
		{"unknown analyzer", "WORKER_ANALYZER", "secret"},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			claudeEnv(t)
			t.Setenv(tc.key, tc.val)
			_, err := LoadWorker()
			if err == nil {
				t.Fatal("invalid claude setting accepted")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), key) {
				t.Fatal("error exposed a configured value")
			}
		})
	}
}

func TestOpenAIAnalyzerConfiguration(t *testing.T) {
	const key = "example-openai-key-not-real"
	openaiEnv := func(t *testing.T) {
		workerEnv(t)
		for _, k := range []string{"WORKER_AI_API_KEY", "WORKER_AI_MODEL", "WORKER_AI_EFFORT"} {
			t.Setenv(k, "")
		}
		t.Setenv("WORKER_ANALYZER", "openai")
		t.Setenv("WORKER_AI_API_KEY", key)
		t.Setenv("WORKER_AI_MODEL", "example-model-1.5")
	}
	t.Run("explicit model and default effort", func(t *testing.T) {
		openaiEnv(t)
		cfg, err := LoadWorker()
		if err != nil || cfg.Analyzer != AnalyzerOpenAI || cfg.AI.APIKey != key ||
			cfg.AI.Model != "example-model-1.5" || cfg.AI.Effort != "high" {
			t.Fatalf("openai config failed: %v", err)
		}
	})
	t.Run("fine-tuned model IDs are accepted", func(t *testing.T) {
		openaiEnv(t)
		t.Setenv("WORKER_AI_MODEL", "ft:example-model:org_1:custom:abc123")
		if _, err := LoadWorker(); err != nil {
			t.Fatalf("fine-tuned model rejected: %v", err)
		}
	})
	t.Run("model is required", func(t *testing.T) {
		openaiEnv(t)
		t.Setenv("WORKER_AI_MODEL", "")
		_, err := LoadWorker()
		if err == nil || !strings.Contains(err.Error(), "WORKER_AI_MODEL") {
			t.Fatal("openai started without an explicit model")
		}
	})
	for _, tc := range []struct{ name, key, val string }{
		{"missing key", "WORKER_AI_API_KEY", ""},
		{"bad effort", "WORKER_AI_EFFORT", "secret"},
		{"bad model", "WORKER_AI_MODEL", "secret model"},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			openaiEnv(t)
			t.Setenv(tc.key, tc.val)
			_, err := LoadWorker()
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), key) {
				t.Fatal("invalid openai setting accepted or exposed")
			}
		})
	}
}

func TestRealAnalyzerTimeouts(t *testing.T) {
	realEnv := func(t *testing.T, analyzer string) {
		workerEnv(t)
		t.Setenv("WORKER_ANALYZER", analyzer)
		t.Setenv("WORKER_AI_API_KEY", "example-key-not-real")
		t.Setenv("WORKER_AI_MODEL", "example-model")
		t.Setenv("WORKER_AI_EFFORT", "")
	}
	for _, analyzer := range []string{AnalyzerClaude, AnalyzerOpenAI} {
		t.Run(analyzer+" defaults allow a slow analysis", func(t *testing.T) {
			realEnv(t, analyzer)
			cfg, err := LoadWorker()
			if err != nil || cfg.Policy.AttemptTimeout != 120*time.Second || cfg.Policy.Lease != 180*time.Second {
				t.Fatalf("real analyzer defaults: %+v %v", cfg.Policy, err)
			}
		})
		t.Run(analyzer+" rejects a short attempt timeout", func(t *testing.T) {
			realEnv(t, analyzer)
			t.Setenv("JOB_ATTEMPT_TIMEOUT", "30s")
			t.Setenv("JOB_LEASE", "60s")
			if _, err := LoadWorker(); err == nil || !strings.Contains(err.Error(), "JOB_ATTEMPT_TIMEOUT") {
				t.Fatalf("short timeout accepted for %s: %v", analyzer, err)
			}
		})
		t.Run(analyzer+" accepts an explicit longer timeout", func(t *testing.T) {
			realEnv(t, analyzer)
			t.Setenv("JOB_ATTEMPT_TIMEOUT", "300s")
			t.Setenv("JOB_LEASE", "400s")
			if cfg, err := LoadWorker(); err != nil || cfg.Policy.AttemptTimeout != 300*time.Second {
				t.Fatalf("explicit timeout rejected: %v", err)
			}
		})
	}
	t.Run("fake keeps its short defaults", func(t *testing.T) {
		workerEnv(t)
		cfg, err := LoadWorker()
		if err != nil || cfg.Policy.AttemptTimeout != 30*time.Second || cfg.Policy.Lease != time.Minute {
			t.Fatal("fake defaults changed")
		}
	})
}

func TestEffortIsOwnedByConfig(t *testing.T) {
	for _, e := range []string{"low", "medium", "high", "xhigh", "max"} {
		if !ValidEffort(e) {
			t.Errorf("%s rejected", e)
		}
	}
	for _, e := range []string{"", "none", "extreme", "HIGH"} {
		if ValidEffort(e) {
			t.Errorf("%q accepted", e)
		}
	}
}
