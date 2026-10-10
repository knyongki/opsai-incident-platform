package database

import (
	"context"
	"strings"
	"testing"
)

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	pool, err := Open(context.Background(), "postgres://user:example%zz@localhost/opsai")
	if pool != nil || err == nil {
		t.Fatal("expected no pool and a configuration error")
	}
	if strings.Contains(err.Error(), "example") {
		t.Fatal("connection details leaked")
	}
}

func TestOpenCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool, err := Open(ctx, "postgres://opsai:example-only@127.0.0.1:5432/opsai?sslmode=disable")
	if pool != nil || err == nil {
		t.Fatal("expected canceled startup to return no pool and an error")
	}
	if strings.Contains(err.Error(), "example-only") {
		t.Fatal("connection details leaked")
	}
}
