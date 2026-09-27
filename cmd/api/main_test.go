package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	httpapi "github.com/knyongki/opsai-incident-platform/internal/transport/http"
)

func TestServeAndGracefulShutdown(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httpapi.NewServer(listener.Addr().String(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serve(ctx, server, listener, logger) }()
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + listener.Addr().String() + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read: %v; close: %v", readErr, closeErr)
	}
	if response.StatusCode != 200 || string(body) != "{\"data\":{\"status\":\"ready\"}}\n" {
		t.Fatalf("unexpected response: %d %s", response.StatusCode, body)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}
