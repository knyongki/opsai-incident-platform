package config

import (
	"strings"
	"testing"
	"time"
)

func workerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"WORKER_ANALYZER", "WORKER_DATABASE_URL", "WORKER_FAKE_OUTCOME", "WORKER_FAKE_DELAY",
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
	if err != nil || cfg.Policy.MaxAttempts != 3 || cfg.Policy.Lease != time.Minute || cfg.Fake.Outcome != "success" {
		t.Fatal("worker defaults failed")
	}
	t.Setenv("JOB_MAX_ATTEMPTS", "4")
	t.Setenv("WORKER_FAKE_OUTCOME", "transient-once")
	cfg, err = LoadWorker()
	if err != nil || cfg.Policy.MaxAttempts != 4 || cfg.Fake.Outcome != "transient-once" {
		t.Fatal("worker overrides failed")
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
