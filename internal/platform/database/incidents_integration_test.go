//go:build integration

package database

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/knyongki/opsai-incident-platform/internal/device"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
)

func integrationID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal("generate fixture UUID failed")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func TestIncidentRepositoryIntegration(t *testing.T) {
	url := os.Getenv("OPSAI_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set OPSAI_TEST_DATABASE_URL to a migrated local test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer pool.Close()
	devices := device.NewService(NewDeviceRepository(pool))
	d, err := devices.Create(ctx, device.Input{Name: device.Change[string]{Set: true, Value: "incident-integration-fixture"}, Type: device.Change[string]{Set: true, Value: "amr"}})
	if err != nil {
		t.Fatal("fixture device creation failed")
	}
	firstID, failedID, missingID, canceledID := integrationID(t), integrationID(t), integrationID(t), integrationID(t)
	ids := []string{firstID, failedID, missingID, canceledID}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		// Only rows owned by this test are deleted; no table-wide cleanup.
		if _, err := pool.Exec(cleanup, "DELETE FROM analysis_jobs WHERE incident_id = ANY($1::uuid[])", ids); err != nil {
			t.Error("job fixture cleanup failed")
		}
		if _, err := pool.Exec(cleanup, "DELETE FROM incidents WHERE id = ANY($1::uuid[])", ids); err != nil {
			t.Error("incident fixture cleanup failed")
		}
		if err := devices.Delete(cleanup, d.ID); err != nil {
			t.Error("device fixture cleanup failed")
		}
	}()
	// Hold an observer connection so the repository necessarily uses another one.
	observer, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("observer connection failed")
	}
	defer observer.Release()
	repo := NewIncidentRepository(pool)
	c := incident.Creation{ID: firstID, JobID: integrationID(t), Input: incident.Input{DeviceID: d.ID, Title: "Fixture incident", Description: "Synthetic test", Severity: "high", RawLogs: []string{}}}
	got, err := repo.Create(ctx, c)
	if err != nil || got.ID != firstID || got.Status != "open" || got.AnalysisStatus != "queued" {
		t.Fatal("atomic creation failed")
	}
	var count int
	var incidentStatus, jobStatus string
	var attempts int
	err = observer.QueryRow(ctx, `SELECT i.status,j.status,j.attempt_count FROM incidents i JOIN analysis_jobs j ON j.incident_id=i.id WHERE i.id=$1`, firstID).Scan(&incidentStatus, &jobStatus, &attempts)
	if err != nil || incidentStatus != "open" || jobStatus != "pending" || attempts != 0 {
		t.Fatal("committed records not visible with expected defaults")
	}
	if err := observer.QueryRow(ctx, "SELECT count(*) FROM analysis_jobs WHERE incident_id=$1", firstID).Scan(&count); err != nil || count != 1 {
		t.Fatal("expected exactly one job")
	}

	// The second incident insert succeeds, then a colliding job PK fails the job insert.
	failed := c
	failed.ID = failedID
	if _, err := repo.Create(ctx, failed); !errors.Is(err, incident.ErrRepository) {
		t.Fatal("expected job insert failure")
	}
	if err := observer.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE id=$1", failedID).Scan(&count); err != nil || count != 0 {
		t.Fatal("incident insert was not rolled back")
	}
	if err := observer.QueryRow(ctx, "SELECT count(*) FROM analysis_jobs WHERE incident_id=$1", failedID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed creation left a job")
	}

	duplicateID, err := uuid(integrationID(t))
	if err != nil {
		t.Fatal("duplicate fixture UUID conversion failed")
	}
	parentID, err := uuid(firstID)
	if err != nil {
		t.Fatal("parent fixture UUID conversion failed")
	}
	_, err = dbgen.New(pool).CreateAnalysisJob(ctx, dbgen.CreateAnalysisJobParams{ID: duplicateID, IncidentID: parentID})
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23505" || pgerr.ConstraintName != "analysis_jobs_incident_id_key" {
		t.Fatal("duplicate initial job not rejected by unique constraint")
	}
	if err := devices.Delete(ctx, d.ID); !errors.Is(err, device.ErrConflict) {
		t.Fatal("referenced device deletion must conflict")
	}

	missing := c
	missing.ID = missingID
	missing.JobID = integrationID(t)
	missing.DeviceID = integrationID(t)
	if _, err := repo.Create(ctx, missing); !errors.Is(err, incident.ErrDeviceNotFound) {
		t.Fatal("missing device mapping failed")
	}
	canceled := c
	canceled.ID = canceledID
	canceled.JobID = integrationID(t)
	canceledCtx, stop := context.WithCancel(ctx)
	stop()
	if _, err := repo.Create(canceledCtx, canceled); err == nil {
		t.Fatal("canceled creation succeeded")
	}
	if err := observer.QueryRow(ctx, "SELECT count(*) FROM incidents WHERE id=ANY($1::uuid[])", []string{missingID, canceledID}).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed creation left incident rows")
	}
}
