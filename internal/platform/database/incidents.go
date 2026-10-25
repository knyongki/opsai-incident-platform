package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
)

type transactionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}
type IncidentRepository struct{ db transactionBeginner }

func NewIncidentRepository(db transactionBeginner) *IncidentRepository {
	return &IncidentRepository{db: db}
}

func incidentStorageError(err error) error {
	var pgerr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgerr) && pgerr.Code == "23503" && pgerr.ConstraintName == "incidents_device_id_fkey") {
		return incident.ErrDeviceNotFound
	}
	return incident.ErrRepository
}

func (r *IncidentRepository) Create(ctx context.Context, c incident.Creation) (result incident.Result, resultErr error) {
	id, err := uuid(c.ID)
	if err != nil {
		return result, incident.ErrInvalid
	}
	jobID, err := uuid(c.JobID)
	if err != nil {
		return result, incident.ErrInvalid
	}
	deviceID, err := uuid(c.DeviceID)
	if err != nil {
		return result, incident.ErrInvalid
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return result, incident.ErrRepository
	}
	defer func() {
		// A canceled request must not prevent bounded transaction cleanup.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			result = incident.Result{}
			resultErr = incident.ErrRepository
		}
	}()
	q := dbgen.New(tx)
	// Friendly not-found check; the FK also protects against concurrent deletion.
	if _, err := q.GetDevice(ctx, deviceID); err != nil {
		return result, incidentStorageError(err)
	}
	occurred := pgtype.Timestamptz{}
	if c.OccurredAt != nil {
		occurred = pgtype.Timestamptz{Time: c.OccurredAt.UTC(), Valid: true}
	}
	row, err := q.CreateIncident(ctx, dbgen.CreateIncidentParams{
		ID: id, DeviceID: deviceID, Title: c.Title, Description: c.Description,
		AlarmCode: nullable(c.AlarmCode), Severity: c.Severity, RawLogs: c.RawLogs, OccurredAt: occurred,
	})
	if err != nil {
		return result, incidentStorageError(err)
	}
	if _, err := q.CreateAnalysisJob(ctx, dbgen.CreateAnalysisJobParams{ID: jobID, IncidentID: row.ID}); err != nil {
		return result, incident.ErrRepository
	}
	if err := tx.Commit(ctx); err != nil {
		return result, incident.ErrRepository
	}
	return incident.Result{ID: c.ID, Status: row.Status, AnalysisStatus: "queued"}, nil
}
