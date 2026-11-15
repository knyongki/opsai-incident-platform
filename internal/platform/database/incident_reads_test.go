package database

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
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

func storedAnalysisJSON(t *testing.T, evidenceID string) []byte {
	t.Helper()
	data, err := json.Marshal(analysis.Result{
		Summary: "Synthetic summary", PrimarySuspectedCause: "No diagnosis was performed",
		ContributingFactors: []string{}, ObservedConditions: []string{"Synthetic input"},
		RecommendedActions: []analysis.Action{}, SafetyNotes: []string{"Test output only"},
		Confidence: 0, Evidence: []analysis.Evidence{{SourceID: evidenceID, Claim: "Synthetic incident record"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestIncidentAnalysisConversion(t *testing.T) {
	const incidentID = "12345678-1234-4234-8234-123456789abc"
	const analysisID = "87654321-4321-4321-8321-cba987654321"
	id, _ := uuid(incidentID)
	aid, _ := uuid(analysisID)
	created := time.Date(2026, 11, 14, 14, 0, 0, 0, time.FixedZone("offset", 7*3600))
	stored := func() dbgen.GetIncidentRow {
		return dbgen.GetIncidentRow{ID: id, AnalysisID: aid,
			AnalysisSchemaVersion: pgtype.Int4{Int32: 1, Valid: true}, AnalysisSource: pgtype.Text{String: "fake", Valid: true},
			AnalysisResult: storedAnalysisJSON(t, incidentID), AnalysisCreatedAt: pgtype.Timestamptz{Time: created, Valid: true}}
	}

	if got, err := incidentAnalysis(dbgen.GetIncidentRow{ID: id}, "completed"); err != nil || got != nil {
		t.Fatal("completed job without a stored row must return null analysis")
	}
	got, err := incidentAnalysis(stored(), "completed")
	if err != nil || got == nil || got.ID != analysisID || got.SchemaVersion != 1 || got.Source != "fake" ||
		got.CreatedAt.Location() != time.UTC || !got.CreatedAt.Equal(created) ||
		got.Result.Summary != "Synthetic summary" || len(got.Result.Evidence) != 1 || got.Result.Evidence[0].SourceID != incidentID ||
		got.ModelName != nil || got.PromptVersion != nil {
		t.Fatalf("stored analysis conversion: %+v %v", got, err)
	}
	claudeRow := stored()
	claudeRow.AnalysisSource.String = "claude"
	claudeRow.AnalysisModelName = pgtype.Text{String: "claude-opus-5-5", Valid: true}
	claudeRow.AnalysisPromptVersion = pgtype.Text{String: "incident-analysis-v1", Valid: true}
	got, err = incidentAnalysis(claudeRow, "completed")
	if err != nil || got == nil || got.Source != "claude" || got.ModelName == nil || *got.ModelName != "claude-opus-5-5" ||
		got.PromptVersion == nil || *got.PromptVersion != "incident-analysis-v1" {
		t.Fatalf("claude analysis conversion: %+v %v", got, err)
	}
	openaiRow := claudeRow
	openaiRow.AnalysisSource.String = "openai"
	openaiRow.AnalysisModelName.String = "example-model"
	if got, err := incidentAnalysis(openaiRow, "completed"); err != nil || got == nil || got.Source != "openai" ||
		*got.ModelName != "example-model" {
		t.Fatalf("openai analysis conversion: %+v %v", got, err)
	}

	for _, tc := range []struct {
		name   string
		status string
		mutate func(*dbgen.GetIncidentRow)
	}{
		{"queued job", "queued", func(*dbgen.GetIncidentRow) {}},
		{"running job", "running", func(*dbgen.GetIncidentRow) {}},
		{"failed job", "failed", func(*dbgen.GetIncidentRow) {}},
		{"unsupported version", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisSchemaVersion.Int32 = 2 }},
		{"missing version", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisSchemaVersion.Valid = false }},
		{"unsupported source", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisSource.String = "model" }},
		{"missing timestamp", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisCreatedAt.Valid = false }},
		{"malformed result", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisResult = []byte(`{"summary":"sensitive stored text"}`) }},
		{"evidence for another incident", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisResult = storedAnalysisJSON(t, analysisID) }},
		{"claude without provenance", "completed", func(r *dbgen.GetIncidentRow) { r.AnalysisSource.String = "claude" }},
		{"fake with provenance", "completed", func(r *dbgen.GetIncidentRow) {
			r.AnalysisModelName = pgtype.Text{String: "claude-opus-5-5", Valid: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := stored()
			tc.mutate(&row)
			got, err := incidentAnalysis(row, tc.status)
			if got != nil || !errors.Is(err, incident.ErrRepository) {
				t.Fatalf("invalid stored analysis accepted: %+v %v", got, err)
			}
		})
	}
}
