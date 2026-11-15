package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/analyzer/claude"
	"github.com/knyongki/opsai-incident-platform/internal/analyzer/openai"
	"github.com/knyongki/opsai-incident-platform/internal/config"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

func TestServeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, func(ctx context.Context) error { close(started); <-ctx.Done(); return nil }, time.Second)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown blocked")
	}
}
func TestServeBoundsUncooperativeWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release := make(chan struct{})
	defer close(release)
	if serve(ctx, func(context.Context) error { <-release; return nil }, time.Millisecond) == nil {
		t.Fatal("shutdown deadline ignored")
	}
}
func TestServeReturnsWorkerError(t *testing.T) {
	stop := errors.New("provider configuration")
	if err := serve(context.Background(), func(context.Context) error { return stop }, time.Second); !errors.Is(err, stop) {
		t.Fatalf("serve returned %v, want the worker error", err)
	}
}

func TestNewAnalyzer(t *testing.T) {
	fake, err := newAnalyzer(config.WorkerConfig{Analyzer: config.AnalyzerFake, Fake: worker.FakeAnalyzer{Outcome: "success"}})
	if _, ok := fake.(worker.FakeAnalyzer); err != nil || !ok {
		t.Fatalf("fake analyzer not selected: %T %v", fake, err)
	}
	claudeAnalyzer, err := newAnalyzer(config.WorkerConfig{Analyzer: config.AnalyzerClaude,
		AI: config.AIConfig{APIKey: "test-key-not-real", Model: "claude-opus-5-5", Effort: "high"}})
	if _, ok := claudeAnalyzer.(*claude.Analyzer); err != nil || !ok {
		t.Fatalf("claude analyzer not selected: %T %v", claudeAnalyzer, err)
	}
	other, err := newAnalyzer(config.WorkerConfig{Analyzer: config.AnalyzerOpenAI,
		AI: config.AIConfig{APIKey: "test-key-not-real", Model: "example-model", Effort: "high"}})
	if _, ok := other.(*openai.Analyzer); err != nil || !ok {
		t.Fatalf("openai analyzer not selected: %T %v", other, err)
	}
	for _, a := range []worker.Analyzer{claudeAnalyzer, other} {
		if _, ok := a.(providerChecker); !ok {
			t.Fatalf("%T has no startup check", a)
		}
	}
	if _, err := newAnalyzer(config.WorkerConfig{Analyzer: "other"}); err == nil {
		t.Fatal("unknown analyzer accepted")
	}
}

func TestCheckFailure(t *testing.T) {
	if err := checkFailure("claude", context.Canceled); err != nil {
		t.Fatalf("Ctrl+C during the startup check must exit cleanly: %v", err)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{worker.ErrProviderConfig, "rejected"},
		{worker.ErrTransient, "unreachable"},
		{context.DeadlineExceeded, "unreachable"},
	} {
		err := checkFailure("openai", tc.err)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "openai") {
			t.Errorf("checkFailure(%v) = %v, want a message containing %q", tc.err, err, tc.want)
		}
	}
	if err := checkFailure("openai", worker.ErrTransient); strings.Contains(err.Error(), "WORKER_AI_API_KEY") {
		t.Error("a network failure must not blame the API key")
	}
}
