package main

import (
	"context"
	"testing"
	"time"
)

func TestServeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serve(ctx, func(ctx context.Context) { close(started); <-ctx.Done() }, time.Second) }()
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
	if serve(ctx, func(context.Context) { <-release }, time.Millisecond) == nil {
		t.Fatal("shutdown deadline ignored")
	}
}
