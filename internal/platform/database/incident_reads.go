package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
)

func incidentReadError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return incident.ErrNotFound
	}
	return incident.ErrRepository
}
func analysisStatus(status pgtype.Text) (string, error) {
	if status.Valid {
		switch status.String {
		case "pending":
			return "queued", nil
		case "running", "completed", "failed":
			return status.String, nil
		}
	}
	// A missing job violates the atomic creation invariant; do not hide the incident.
	return "", incident.ErrRepository
}
func uuidString(id pgtype.UUID) string {
	b := id.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func timePointer(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	utc := t.Time.UTC()
	return &utc
}
func textPointer(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}
func incidentSummary(row dbgen.ListIncidentsRow) (incident.Summary, error) {
	status, err := analysisStatus(row.JobStatus)
	if err != nil {
		return incident.Summary{}, err
	}
	return incident.Summary{
		ID: uuidString(row.ID), DeviceID: uuidString(row.DeviceID), Title: row.Title,
		AlarmCode: textPointer(row.AlarmCode), Severity: row.Severity, Status: row.Status,
		AnalysisStatus: status, OccurredAt: timePointer(row.OccurredAt),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}, nil
}
func (r *IncidentRepository) List(ctx context.Context, query incident.ListQuery) ([]incident.Summary, error) {
	params := dbgen.ListIncidentsParams{
		Status:    pgtype.Text{String: query.Status, Valid: query.Status != ""},
		Severity:  pgtype.Text{String: query.Severity, Valid: query.Severity != ""},
		AlarmCode: nullable(query.AlarmCode), RowLimit: int32(query.Limit),
	}
	if query.DeviceID != "" {
		var err error
		params.DeviceID, err = uuid(query.DeviceID)
		if err != nil {
			return nil, incident.ErrInvalid
		}
	}
	if query.Before != nil {
		var err error
		params.BeforeID, err = uuid(query.Before.ID)
		if err != nil {
			return nil, incident.ErrInvalid
		}
		params.BeforeCreatedAt = pgtype.Timestamptz{Time: query.Before.CreatedAt, Valid: true}
	}
	rows, err := dbgen.New(r.db).ListIncidents(ctx, params)
	if err != nil {
		return nil, incident.ErrRepository
	}
	items := make([]incident.Summary, 0, len(rows))
	for _, row := range rows {
		item, err := incidentSummary(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
func (r *IncidentRepository) Get(ctx context.Context, id string) (incident.Detail, error) {
	key, err := uuid(id)
	if err != nil {
		return incident.Detail{}, incident.ErrInvalid
	}
	row, err := dbgen.New(r.db).GetIncident(ctx, key)
	if err != nil {
		return incident.Detail{}, incidentReadError(err)
	}
	summary, err := incidentSummary(dbgen.ListIncidentsRow{
		ID: row.ID, DeviceID: row.DeviceID, Title: row.Title, AlarmCode: row.AlarmCode,
		Severity: row.Severity, Status: row.Status, OccurredAt: row.OccurredAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, JobStatus: row.JobStatus,
	})
	if err != nil {
		return incident.Detail{}, err
	}
	logs := row.RawLogs
	if logs == nil {
		logs = []string{}
	}
	return incident.Detail{Summary: summary, Description: row.Description, RawLogs: logs, ResolvedAt: timePointer(row.ResolvedAt)}, nil
}
