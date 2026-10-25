package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
)

type failingBeginner struct{ dbgen.DBTX }

func (failingBeginner) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("sensitive driver text")
}
func TestIncidentStorageErrors(t *testing.T) {
	cases := []struct{ input, want error }{
		{pgx.ErrNoRows, incident.ErrDeviceNotFound},
		{&pgconn.PgError{Code: "23503", ConstraintName: "incidents_device_id_fkey"}, incident.ErrDeviceNotFound},
		{&pgconn.PgError{Code: "23503", ConstraintName: "other"}, incident.ErrRepository},
		{errors.New("sensitive driver text"), incident.ErrRepository},
	}
	for _, tc := range cases {
		if !errors.Is(incidentStorageError(tc.input), tc.want) {
			t.Fatal("wrong sanitized mapping")
		}
	}
	id := "12345678-1234-4234-8234-123456789abc"
	_, err := NewIncidentRepository(failingBeginner{}).Create(context.Background(), incident.Creation{ID: id, JobID: id, Input: incident.Input{DeviceID: id}})
	if !errors.Is(err, incident.ErrRepository) {
		t.Fatal("begin error not sanitized")
	}
}
