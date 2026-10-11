package database

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/knyongki/opsai-incident-platform/internal/device"
	"testing"
)

func TestStorageError(t *testing.T) {
	for _, tt := range []struct{ in, want error }{{nil, nil}, {pgx.ErrNoRows, device.ErrNotFound}, {&pgconn.PgError{Code: "23505", Message: "secret"}, device.ErrConflict}, {&pgconn.PgError{Code: "08006", Message: "secret"}, device.ErrRepository}, {errors.New("secret"), device.ErrRepository}} {
		if got := storageError(tt.in); got != tt.want {
			t.Fatalf("unexpected mapping: %v", got)
		}
	}
}
