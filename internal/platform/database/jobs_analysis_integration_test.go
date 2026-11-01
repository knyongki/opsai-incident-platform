//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

func analysisFixture(id string) analysis.Result {
	return analysis.Result{
		Summary: "Synthetic test result", PrimarySuspectedCause: "No diagnosis performed",
		ContributingFactors: []string{}, ObservedConditions: []string{"Synthetic input"},
		RecommendedActions: []analysis.Action{}, SafetyNotes: []string{"Test output only"},
		Confidence: 0, Evidence: []analysis.Evidence{{SourceID: id, Claim: "Synthetic incident"}},
	}
}

func TestFinishWithResultIntegration(t *testing.T) {
	pool, ctx := workerTestPool(t)
	var queued int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analysis_jobs WHERE status IN ('pending','running')").Scan(&queued); err != nil || queued != 0 {
		t.Fatal("worker test queue must be empty; use a dedicated database with no running workers")
	}
	policy := worker.DefaultPolicy()
	repo, err := NewJobRepository(pool, policy)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("atomic completion is visible on another connection", func(t *testing.T) {
		jobFixtures(t, pool, ctx, 1)
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal("observer connection unavailable")
		}
		defer conn.Release()
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); err != nil {
			t.Fatal("atomic finish failed")
		}
		var status, source, stored string
		var version int
		if err := conn.QueryRow(ctx, `SELECT j.status,a.source,a.schema_version,a.result::text
			FROM analysis_jobs j JOIN incident_analyses a ON a.incident_id=j.incident_id WHERE j.id=$1`, job.ID).
			Scan(&status, &source, &version, &stored); err != nil {
			t.Fatal("committed result unavailable")
		}
		var decoded analysis.Result
		if err := json.Unmarshal([]byte(stored), &decoded); err != nil || status != "completed" || source != "fake" || version != 1 || decoded.Summary != "Synthetic test result" {
			t.Fatal("result and completed job did not commit together")
		}
		if err := analysis.Validate(decoded, job.IncidentID); err != nil {
			t.Fatal("stored result failed validation")
		}
	})

	t.Run("invalid result is rejected before writing", func(t *testing.T) {
		jobFixtures(t, pool, ctx, 1)
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		bad := analysisFixture(job.IncidentID)
		bad.Confidence = 2
		if err := repo.FinishWithResult(ctx, job, bad); !errors.Is(err, analysis.ErrInvalid) {
			t.Fatal("invalid result accepted")
		}
		assertNoAnalysis(t, pool, ctx, job.IncidentID)
		assertJob(t, pool, ctx, job.ID, "running", job.Attempt)
	})

	for _, stage := range []string{"insert", "finish"} {
		t.Run("rollback after "+stage+" failure", func(t *testing.T) {
			jobFixtures(t, pool, ctx, 1)
			job, err := repo.Claim(ctx)
			if err != nil {
				t.Fatal("claim failed")
			}
			observer, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal("observer connection unavailable")
			}
			defer observer.Release()
			faulty, err := NewJobRepository(execFaultDB{transactionBeginner: pool, fail: stage}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := faulty.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); !errors.Is(err, worker.ErrRepository) {
				t.Fatal("injected persistence failure was not returned")
			}
			assertNoAnalysis(t, observer, ctx, job.IncidentID)
			assertJob(t, observer, ctx, job.ID, "running", job.Attempt)
		})
	}

	t.Run("expired and stale owners cannot write", func(t *testing.T) {
		jobFixtures(t, pool, ctx, 1)
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		expireJob(t, pool, ctx, job.ID)
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); !errors.Is(err, worker.ErrOwnership) {
			t.Fatal("expired owner accepted")
		}
		assertNoAnalysis(t, pool, ctx, job.IncidentID)
		if err := repo.Recover(ctx); err != nil {
			t.Fatal("recovery failed")
		}
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET available_at='2020-01-01' WHERE id=$1", job.ID)
		fresh, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("reclaim failed")
		}
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); !errors.Is(err, worker.ErrOwnership) {
			t.Fatal("stale owner accepted")
		}
		if err := repo.FinishWithResult(ctx, fresh, analysisFixture(fresh.IncidentID)); err != nil {
			t.Fatal("new owner could not finish")
		}
	})

	t.Run("duplicate result cannot be overwritten", func(t *testing.T) {
		jobFixtures(t, pool, ctx, 1)
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		if _, err := pool.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result)
			VALUES($1,$2,1,'fake','{"marker":"original"}')`, integrationID(t), job.IncidentID); err != nil {
			t.Fatal("seed existing analysis failed")
		}
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); !errors.Is(err, worker.ErrRepository) {
			t.Fatal("duplicate result was treated as success")
		}
		var result string
		if err := pool.QueryRow(ctx, "SELECT result->>'marker' FROM incident_analyses WHERE incident_id=$1", job.IncidentID).Scan(&result); err != nil || result != "original" {
			t.Fatal("existing result was overwritten")
		}
		assertJob(t, pool, ctx, job.ID, "running", job.Attempt)
	})

	for _, after := range []bool{false, true} {
		name := "commit failure rolls back"
		if after {
			name = "lost commit acknowledgement leaves one result"
		}
		t.Run(name, func(t *testing.T) {
			jobFixtures(t, pool, ctx, 1)
			job, err := repo.Claim(ctx)
			if err != nil {
				t.Fatal("claim failed")
			}
			observer, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal("observer connection unavailable")
			}
			defer observer.Release()
			faulty, err := NewJobRepository(commitFaultDB{transactionBeginner: pool, after: after}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := faulty.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); !errors.Is(err, worker.ErrRepository) {
				t.Fatal("commit failure was not surfaced")
			}
			if after {
				assertJob(t, observer, ctx, job.ID, "completed", job.Attempt)
				var count int
				if err := observer.QueryRow(ctx, "SELECT count(*) FROM incident_analyses WHERE incident_id=$1", job.IncidentID).Scan(&count); err != nil || count != 1 {
					t.Fatal("ambiguous commit did not leave exactly one analysis")
				}
			} else {
				assertJob(t, observer, ctx, job.ID, "running", job.Attempt)
				assertNoAnalysis(t, observer, ctx, job.IncidentID)
			}
		})
	}
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func assertNoAnalysis(t *testing.T, db queryRower, ctx context.Context, incidentID string) {
	t.Helper()
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM incident_analyses WHERE incident_id=$1", incidentID).Scan(&count); err != nil || count != 0 {
		t.Fatal("unexpected analysis row")
	}
}

type execFaultDB struct {
	transactionBeginner
	fail string
}

func (d execFaultDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := d.transactionBeginner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return execFaultTx{Tx: tx, fail: d.fail}, nil
}

type execFaultTx struct {
	pgx.Tx
	fail string
}

func (tx execFaultTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if tx.fail == "insert" && strings.Contains(query, "INSERT INTO incident_analyses") ||
		tx.fail == "finish" && strings.Contains(query, "UPDATE analysis_jobs") {
		return pgconn.CommandTag{}, errors.New("synthetic transaction failure")
	}
	return tx.Tx.Exec(ctx, query, args...)
}

func TestIncidentAnalysisMigrationIntegration(t *testing.T) {
	pool, ctx := workerTestPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin migration test failed")
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Error("migration schema cleanup failed")
		}
	}()
	schema := "analysis_test_" + strings.ReplaceAll(integrationID(t), "-", "")
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema); err != nil {
		t.Fatal("isolated schema failed")
	}
	for _, name := range []string{"00001_create_devices.sql", "00002_create_incidents_and_analysis_jobs.sql", "00003_add_analysis_job_leases.sql"} {
		if _, err := tx.Exec(ctx, migrationUp(t, name)); err != nil {
			t.Fatal("baseline migration failed")
		}
	}
	statuses := []string{"pending", "completed", "failed"}
	ids := make([]string, len(statuses))
	for i, status := range statuses {
		id := integrationID(t)
		ids[i] = id
		if _, err := tx.Exec(ctx, "INSERT INTO devices(id,name,type) VALUES($1,'synthetic','amr')", id); err != nil {
			t.Fatal("device seed failed")
		}
		if _, err := tx.Exec(ctx, "INSERT INTO incidents(id,device_id,title,description,severity) VALUES($1,$1,'synthetic','synthetic','high')", id); err != nil {
			t.Fatal("incident seed failed")
		}
		if _, err := tx.Exec(ctx, "INSERT INTO analysis_jobs(id,incident_id,status) VALUES($1,$1,$2)", id, status); err != nil {
			t.Fatal("job seed failed")
		}
	}
	if _, err := tx.Exec(ctx, migrationUp(t, "00004_create_incident_analyses.sql")); err != nil {
		t.Fatal("analysis migration failed")
	}
	var jobs, analyses int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM analysis_jobs").Scan(&jobs); err != nil || jobs != len(statuses) {
		t.Fatal("legacy jobs changed during migration")
	}
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM incident_analyses").Scan(&analyses); err != nil || analyses != 0 {
		t.Fatal("migration fabricated legacy results")
	}
	for i, status := range statuses {
		var got string
		if err := tx.QueryRow(ctx, "SELECT status FROM analysis_jobs WHERE id=$1", ids[i]).Scan(&got); err != nil || got != status {
			t.Fatal("legacy job status changed")
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result) VALUES($1,$2,1,'fake','{}')`, integrationID(t), ids[0]); err != nil {
		t.Fatal("valid schema row rejected")
	}
	for _, values := range []struct {
		version        int
		source, result string
	}{{2, "fake", "{}"}, {1, "real", "{}"}, {1, "fake", "[]"}} {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal("begin constraint check failed")
		}
		if _, err := sp.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result) VALUES($1,$2,$3,$4,$5::jsonb)`, integrationID(t), ids[1], values.version, values.source, values.result); err == nil {
			t.Fatal("invalid schema row accepted")
		}
		if err := sp.Rollback(ctx); err != nil {
			t.Fatal("rollback constraint check failed")
		}
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal("begin unique check failed")
	}
	if _, err := sp.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result) VALUES($1,$2,1,'fake','{}')`, integrationID(t), ids[0]); err == nil {
		t.Fatal("duplicate incident analysis accepted")
	}
	if err := sp.Rollback(ctx); err != nil {
		t.Fatal("rollback unique check failed")
	}
}
