package database

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
	"testing"
	"time"
)

func TestAnalysisStatusMapping(t *testing.T) {
	for db, want := range map[string]string{"pending": "queued", "running": "running", "completed": "completed", "failed": "failed"} {
		got, err := analysisStatus(pgtype.Text{String: db, Valid: true})
		if err != nil || got != want {
			t.Fatalf("%s => %s %v", db, got, err)
		}
	}
	for _, value := range []pgtype.Text{{}, {String: "unknown", Valid: true}} {
		if _, err := analysisStatus(value); !errors.Is(err, incident.ErrRepository) {
			t.Fatal("invalid/missing job status accepted")
		}
	}
}
func TestIncidentReadConversion(t *testing.T) {
	id, _ := uuid("12345678-1234-4234-8234-123456789abc")
	now := time.Date(2026, 1, 1, 10, 0, 0, 123456000, time.FixedZone("offset", 7*3600))
	row := dbgen.ListIncidentsRow{ID: id, DeviceID: id, Title: "Summary", Severity: "high", Status: "open", JobStatus: pgtype.Text{String: "pending", Valid: true}, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	got, err := incidentSummary(row)
	if err != nil || got.AlarmCode != nil || got.OccurredAt != nil || got.ID != "12345678-1234-4234-8234-123456789abc" || got.AnalysisStatus != "queued" || got.CreatedAt.Location() != time.UTC || !got.CreatedAt.Equal(now) {
		t.Fatal("nullable summary conversion")
	}
	row.AlarmCode = pgtype.Text{String: "", Valid: true}
	row.OccurredAt = row.CreatedAt
	got, err = incidentSummary(row)
	if err != nil || got.AlarmCode == nil || *got.AlarmCode != "" || got.OccurredAt == nil || !got.OccurredAt.Equal(now) {
		t.Fatal("present empty/null conversion")
	}
	if !errors.Is(incidentReadError(pgx.ErrNoRows), incident.ErrNotFound) || !errors.Is(incidentReadError(errors.New("sensitive driver error")), incident.ErrRepository) {
		t.Fatal("read error mapping")
	}
}
