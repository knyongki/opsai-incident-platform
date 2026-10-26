//go:build integration

package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/knyongki/opsai-incident-platform/internal/config"
	"github.com/knyongki/opsai-incident-platform/internal/device"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

func workerTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	raw := os.Getenv("OPSAI_WORKER_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set OPSAI_WORKER_TEST_DATABASE_URL to a dedicated migrated worker test database")
	}
	if config.ValidateWorkerDatabaseURL(raw) != nil {
		t.Fatal("worker tests require an isolated localhost database ending in _worker_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	pool, err := Open(ctx, raw)
	if err != nil {
		cancel()
		t.Fatal("worker database unavailable")
	}
	t.Cleanup(func() { pool.Close(); cancel() })
	return pool, ctx
}
func jobFixtures(t *testing.T, pool *pgxpool.Pool, ctx context.Context, n int) []string {
	t.Helper()
	ds := device.NewService(NewDeviceRepository(pool))
	d, err := ds.Create(ctx, device.Input{Name: device.Change[string]{Set: true, Value: "Worker synthetic fixture"}, Type: device.Change[string]{Set: true, Value: "amr"}})
	if err != nil {
		t.Fatal("device fixture failed")
	}
	ids := make([]string, 0, n)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanup, "DELETE FROM analysis_jobs WHERE incident_id IN (SELECT id FROM incidents WHERE device_id=$1)", d.ID); err != nil {
			t.Error("job cleanup failed")
		}
		if _, err := pool.Exec(cleanup, "DELETE FROM incidents WHERE device_id=$1", d.ID); err != nil {
			t.Error("incident cleanup failed")
		}
		if err := ds.Delete(cleanup, d.ID); err != nil {
			t.Error("device cleanup failed")
		}
	})
	for i := 0; i < n; i++ {
		id := integrationID(t)
		_, err := NewIncidentRepository(pool).Create(ctx, incident.Creation{ID: integrationID(t), JobID: id, Input: incident.Input{
			DeviceID: d.ID, Title: "Synthetic worker incident", Description: "Synthetic fixture", Severity: "high", RawLogs: []string{}}})
		if err != nil {
			t.Fatal("job fixture failed")
		}
		ids = append(ids, id)
	}
	return ids
}
func jobExec(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatal("job fixture mutation failed")
	}
}
func assertJob(t *testing.T, pool *pgxpool.Pool, ctx context.Context, id, status string, attempt int) {
	t.Helper()
	var got string
	var count int
	var clean bool
	err := pool.QueryRow(ctx, "SELECT status,attempt_count,(claim_token IS NULL AND locked_at IS NULL AND lease_expires_at IS NULL) FROM analysis_jobs WHERE id=$1", id).Scan(&got, &count, &clean)
	if err != nil || got != status || count != attempt || (status != "running" && !clean) {
		t.Fatalf("job state mismatch: status=%s attempt=%d clean=%v", got, count, clean)
	}
}
func expireJob(t *testing.T, pool *pgxpool.Pool, ctx context.Context, id string) {
	t.Helper()
	jobExec(t, pool, ctx, "UPDATE analysis_jobs SET locked_at=clock_timestamp()-interval '2 minutes',lease_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1", id)
}
func TestJobRepositoryIntegration(t *testing.T) {
	pool, ctx := workerTestPool(t)
	var queued int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analysis_jobs WHERE status IN ('pending','running')").Scan(&queued); err != nil || queued != 0 {
		t.Fatal("worker test queue must be empty; use a dedicated database with no running workers")
	}
	p := worker.DefaultPolicy()
	p.RetryBase = time.Millisecond
	p.RetryCap = 10 * time.Millisecond
	repo, err := NewJobRepository(pool, p)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ordering and skip locked", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 3)
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET available_at='2020-01-01' WHERE id=ANY($1::uuid[])", ids[:2])
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET available_at=clock_timestamp()+interval '1 hour' WHERE id=$1", ids[2])
		first, second := ids[0], ids[1]
		if first > second {
			first, second = second, first
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal("begin lock failed")
		}
		if _, err = tx.Exec(ctx, "SELECT id FROM analysis_jobs WHERE id=$1 FOR UPDATE", first); err != nil {
			t.Fatal("lock failed")
		}
		j, err := repo.Claim(ctx)
		if err != nil || j.ID != second {
			t.Fatal("locked oldest row blocked another due job")
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal("unlock failed")
		}
		j2, err := repo.Claim(ctx)
		if err != nil || j2.ID != first {
			t.Fatal("UUID ordering failed")
		}
		if _, err = repo.Claim(ctx); !errors.Is(err, worker.ErrEmpty) {
			t.Fatal("future job claimed")
		}
	})
	t.Run("competing connections", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		start := make(chan struct{})
		var wg sync.WaitGroup
		jobs := make(chan worker.Job, 2)
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; j, e := repo.Claim(ctx); jobs <- j; errs <- e }()
		}
		close(start)
		wg.Wait()
		close(jobs)
		close(errs)
		won, empty := 0, 0
		for e := range errs {
			if e == nil {
				won++
			} else if errors.Is(e, worker.ErrEmpty) {
				empty++
			} else {
				t.Fatal("claim failed")
			}
		}
		if won != 1 || empty != 1 {
			t.Fatal("duplicate ownership")
		}
		for j := range jobs {
			if j.ID != "" && j.ID != ids[0] {
				t.Fatal("claimed unrelated job")
			}
		}
		assertJob(t, pool, ctx, ids[0], "running", 1)
	})
	t.Run("recovery and stale fencing", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		old, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		expireJob(t, pool, ctx, old.ID)
		if err = repo.Finish(ctx, old, worker.Outcome{Status: "completed"}); !errors.Is(err, worker.ErrOwnership) {
			t.Fatal("expired owner accepted")
		}
		if err = repo.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		assertJob(t, pool, ctx, ids[0], "pending", 1)
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET available_at='2020-01-01' WHERE id=$1", old.ID)
		fresh, err := repo.Claim(ctx)
		if err != nil || fresh.Token == old.Token || fresh.Attempt != 2 {
			t.Fatal("new claim token/attempt missing")
		}
		for _, o := range []worker.Outcome{{Status: "completed"}, p.Failure(1, "ANALYSIS_TRANSIENT", true), p.Failure(1, "ANALYSIS_PERMANENT", false)} {
			if err = repo.Finish(ctx, old, o); !errors.Is(err, worker.ErrOwnership) {
				t.Fatal("stale owner wrote state")
			}
		}
		if err = repo.Finish(ctx, fresh, worker.Outcome{Status: "completed"}); err != nil {
			t.Fatal(err)
		}
		assertJob(t, pool, ctx, ids[0], "completed", 2)
		detail, err := NewIncidentRepository(pool).Get(ctx, fresh.IncidentID)
		if err != nil || detail.Status != "open" || detail.AnalysisStatus != "completed" {
			t.Fatal("fake completion changed incident or API mapping")
		}
	})
	t.Run("retry then success and permanent failure", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		j, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = repo.Finish(ctx, j, p.Failure(j.Attempt, "ANALYSIS_TRANSIENT", true)); err != nil {
			t.Fatal(err)
		}
		var delayed bool
		if err = pool.QueryRow(ctx, "SELECT available_at>updated_at FROM analysis_jobs WHERE id=$1", j.ID).Scan(&delayed); err != nil || !delayed {
			t.Fatal("retry not delayed")
		}
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET available_at='2020-01-01' WHERE id=$1", j.ID)
		j, err = repo.Claim(ctx)
		if err != nil || j.Attempt != 2 {
			t.Fatal("retry attempt mismatch")
		}
		if err = repo.Finish(ctx, j, p.Result(j.Attempt, nil)); err != nil {
			t.Fatal(err)
		}
		assertJob(t, pool, ctx, ids[0], "completed", 2)
		jobFixtures(t, pool, ctx, 1)
		j, err = repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = repo.Finish(ctx, j, p.Result(j.Attempt, worker.ErrPermanent)); err != nil {
			t.Fatal(err)
		}
		assertJob(t, pool, ctx, j.ID, "failed", 1)
	})
	t.Run("exhaustion after crash or policy change", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 2)
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET attempt_count=$2 WHERE id=$1", ids[0], p.MaxAttempts-1)
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET attempt_count=$2 WHERE id=$1", ids[1], p.MaxAttempts)
		j, err := repo.Claim(ctx)
		if err != nil || j.ID != ids[0] || j.Attempt != 3 {
			t.Fatal("last claim failed")
		}
		expireJob(t, pool, ctx, j.ID)
		if err = repo.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			assertJob(t, pool, ctx, id, "failed", 3)
		}
		if _, err = repo.Claim(ctx); !errors.Is(err, worker.ErrEmpty) {
			t.Fatal("exhausted job reclaimed")
		}
	})
	t.Run("commit failures", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		bad, _ := NewJobRepository(commitFaultDB{transactionBeginner: pool}, p)
		j, err := bad.Claim(ctx)
		if !errors.Is(err, worker.ErrRepository) || j.ID != "" {
			t.Fatal("uncommitted claim exposed")
		}
		assertJob(t, pool, ctx, ids[0], "pending", 0)
		j, err = repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ambiguous, _ := NewJobRepository(commitFaultDB{transactionBeginner: pool, after: true}, p)
		if err = ambiguous.Finish(ctx, j, worker.Outcome{Status: "completed"}); !errors.Is(err, worker.ErrRepository) {
			t.Fatal("lost acknowledgement not surfaced")
		}
		assertJob(t, pool, ctx, j.ID, "completed", 1)
		if err = repo.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.Claim(ctx); !errors.Is(err, worker.ErrEmpty) {
			t.Fatal("completed job repeated")
		}
	})
	t.Run("lease expiry while waiting for row lock", func(t *testing.T) {
		jobFixtures(t, pool, ctx, 1)
		j, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal("lock transaction failed")
		}
		if _, err = tx.Exec(ctx, "UPDATE analysis_jobs SET locked_at=clock_timestamp()-interval '2 minutes',lease_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1", j.ID); err != nil {
			t.Fatal("expire under lock failed")
		}
		result := make(chan error, 1)
		go func() { result <- repo.Finish(ctx, j, worker.Outcome{Status: "completed"}) }()
		if err = tx.Commit(ctx); err != nil {
			t.Fatal("commit expiry failed")
		}
		if err = <-result; !errors.Is(err, worker.ErrOwnership) {
			t.Fatal("late lock acquisition extended lease")
		}
	})
	t.Run("ownership constraint", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		if _, err := pool.Exec(ctx, "UPDATE analysis_jobs SET status='running' WHERE id=$1", ids[0]); err == nil {
			t.Fatal("running without lease accepted")
		}
		if _, err := pool.Exec(ctx, "UPDATE analysis_jobs SET claim_token=id WHERE id=$1", ids[0]); err == nil {
			t.Fatal("pending with token accepted")
		}
	})
}

type commitFaultDB struct {
	transactionBeginner
	after bool
}

func (d commitFaultDB) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := d.transactionBeginner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return commitFaultTx{Tx: tx, after: d.after}, nil
}

type commitFaultTx struct {
	pgx.Tx
	after bool
}

func (tx commitFaultTx) Commit(ctx context.Context) error {
	if tx.after {
		if err := tx.Tx.Commit(ctx); err != nil {
			return err
		}
	}
	return errors.New("synthetic private driver failure")
}

func migrationUp(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", name))
	if err != nil {
		t.Fatal("read migration failed")
	}
	return strings.Split(string(b), "-- +goose Down")[0]
}
func TestJobLeaseMigrationIntegration(t *testing.T) {
	pool, ctx := workerTestPool(t)
	for _, state := range []string{"pending", "completed", "failed", "running", "pending-locked"} {
		t.Run(state, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal("begin migration fixture failed")
			}
			defer func() {
				if err := tx.Rollback(ctx); err != nil {
					t.Error("migration fixture rollback failed")
				}
			}()
			schema := "lease_test_" + strings.ReplaceAll(integrationID(t), "-", "")
			if _, err = tx.Exec(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema); err != nil {
				t.Fatal("isolated schema failed")
			}
			for _, name := range []string{"00001_create_devices.sql", "00002_create_incidents_and_analysis_jobs.sql"} {
				if _, err = tx.Exec(ctx, migrationUp(t, name)); err != nil {
					t.Fatal("baseline migration failed")
				}
			}
			id := integrationID(t)
			if _, err = tx.Exec(ctx, "INSERT INTO devices(id,name,type) VALUES($1,'synthetic','amr')", id); err != nil {
				t.Fatal("device seed failed")
			}
			if _, err = tx.Exec(ctx, "INSERT INTO incidents(id,device_id,title,description,severity) VALUES($1,$1,'synthetic','synthetic','high')", id); err != nil {
				t.Fatal("incident seed failed")
			}
			status := state
			if state == "pending-locked" {
				status = "pending"
			}
			if _, err = tx.Exec(ctx, "INSERT INTO analysis_jobs(id,incident_id,status) VALUES($1,$1,$2)", id, status); err != nil {
				t.Fatal("job seed failed")
			}
			if state == "pending-locked" {
				if _, err = tx.Exec(ctx, "UPDATE analysis_jobs SET locked_at=now()"); err != nil {
					t.Fatal("lock seed failed")
				}
			}
			_, err = tx.Exec(ctx, migrationUp(t, "00003_add_analysis_job_leases.sql"))
			incompatible := state == "running" || state == "pending-locked"
			if incompatible != (err != nil) {
				t.Fatal("migration compatibility mismatch")
			}
			if !incompatible {
				if _, err = tx.Exec(ctx, "SELECT claim_token,lease_expires_at FROM analysis_jobs"); err != nil {
					t.Fatal("migration columns missing")
				}
			}
		})
	}
}
