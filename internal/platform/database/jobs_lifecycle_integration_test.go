//go:build integration

package database

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

func incidentStatusForJob(t *testing.T, db queryRower, ctx context.Context, jobID string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(ctx, `SELECT i.status FROM incidents i JOIN analysis_jobs j ON j.incident_id=i.id WHERE j.id=$1`, jobID).Scan(&status); err != nil {
		t.Fatal("incident status lookup failed")
	}
	return status
}

func assertIncidentStatus(t *testing.T, db queryRower, ctx context.Context, jobID, want string) {
	t.Helper()
	if got := incidentStatusForJob(t, db, ctx, jobID); got != want {
		t.Fatalf("incident status = %s, want %s", got, want)
	}
}

func makeAvailable(t *testing.T, pool *pgxpool.Pool, ctx context.Context, jobID string) {
	t.Helper()
	jobExec(t, pool, ctx, "UPDATE analysis_jobs SET available_at='2020-01-01' WHERE id=$1", jobID)
}

func TestIncidentLifecycleIntegration(t *testing.T) {
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
	claim := func(t *testing.T, r *JobRepository) worker.Job {
		t.Helper()
		job, err := r.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		return job
	}

	t.Run("claim, retry, and stored result", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "open")
		job := claim(t, repo)
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzing")
		if err := repo.Finish(ctx, job, p.Failure(job.Attempt, "ANALYSIS_TRANSIENT", true)); err != nil {
			t.Fatal("retry failed")
		}
		assertJob(t, pool, ctx, ids[0], "pending", 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzing")
		makeAvailable(t, pool, ctx, ids[0])
		job = claim(t, repo)
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); err != nil {
			t.Fatal("finish with result failed")
		}
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzed")
		var updated, created time.Time
		if err := pool.QueryRow(ctx, `SELECT i.updated_at,i.created_at FROM incidents i JOIN analysis_jobs j ON j.incident_id=i.id WHERE j.id=$1`, ids[0]).
			Scan(&updated, &created); err != nil || !updated.After(created) {
			t.Fatal("incident updated_at did not move with the transition")
		}
	})

	t.Run("permanent failure", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		job := claim(t, repo)
		if err := repo.Finish(ctx, job, p.Failure(job.Attempt, "ANALYSIS_PERMANENT", false)); err != nil {
			t.Fatal("permanent failure was not recorded")
		}
		assertJob(t, pool, ctx, ids[0], "failed", 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "analysis_failed")
	})

	t.Run("retry on the last attempt becomes terminal", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET attempt_count=$2 WHERE id=$1", ids[0], p.MaxAttempts-1)
		job := claim(t, repo)
		if err := repo.Finish(ctx, job, worker.Outcome{Status: "pending", Code: "ANALYSIS_TRANSIENT", Delay: time.Millisecond}); err != nil {
			t.Fatal("final retry was not recorded")
		}
		assertJob(t, pool, ctx, ids[0], "failed", p.MaxAttempts)
		assertIncidentStatus(t, pool, ctx, ids[0], "analysis_failed")
	})

	t.Run("invalid output from the worker", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		makeAvailable(t, pool, ctx, ids[0])
		w, err := worker.New(repo, worker.FakeAnalyzer{Outcome: "invalid-output"}, p, slog.Default())
		if err != nil || w.Step(ctx) != nil {
			t.Fatal("worker step failed")
		}
		assertJob(t, pool, ctx, ids[0], "failed", 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "analysis_failed")
		assertNoAnalysis(t, pool, ctx, workerResultDetail(t, pool, ctx, ids[0]).ID)
	})

	t.Run("lease recovery", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		job := claim(t, repo)
		expireJob(t, pool, ctx, job.ID)
		if err := repo.Recover(ctx); err != nil {
			t.Fatal("recovery failed")
		}
		assertJob(t, pool, ctx, ids[0], "pending", 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzing")

		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET attempt_count=$2 WHERE id=$1", ids[0], p.MaxAttempts-1)
		makeAvailable(t, pool, ctx, ids[0])
		job = claim(t, repo)
		expireJob(t, pool, ctx, job.ID)
		if err := repo.Recover(ctx); err != nil {
			t.Fatal("final recovery failed")
		}
		assertJob(t, pool, ctx, ids[0], "failed", p.MaxAttempts)
		assertIncidentStatus(t, pool, ctx, ids[0], "analysis_failed")
	})

	t.Run("resolved incident is never overwritten", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 2)
		for _, id := range ids {
			jobExec(t, pool, ctx, "UPDATE incidents SET status='resolved' WHERE id=(SELECT incident_id FROM analysis_jobs WHERE id=$1)", id)
		}
		first := claim(t, repo)
		if err := repo.FinishWithResult(ctx, first, analysisFixture(first.IncidentID)); err != nil {
			t.Fatal("finish with result failed")
		}
		second := claim(t, repo)
		if err := repo.Finish(ctx, second, p.Failure(second.Attempt, "ANALYSIS_PERMANENT", false)); err != nil {
			t.Fatal("failure was not recorded")
		}
		for _, id := range ids {
			assertIncidentStatus(t, pool, ctx, id, "resolved")
		}
		assertJob(t, pool, ctx, first.ID, "completed", 1)
		assertJob(t, pool, ctx, second.ID, "failed", 1)
	})

	t.Run("failed incident update rolls back the job change", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		faulty, err := NewJobRepository(execFaultDB{transactionBeginner: pool, fail: "incident"}, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := faulty.Claim(ctx); !errors.Is(err, worker.ErrRepository) {
			t.Fatal("claim succeeded without its incident update")
		}
		assertJob(t, pool, ctx, ids[0], "pending", 0)
		assertIncidentStatus(t, pool, ctx, ids[0], "open")

		job := claim(t, repo)
		if err := faulty.FinishWithResult(ctx, job, analysisFixture(job.IncidentID)); !errors.Is(err, worker.ErrRepository) {
			t.Fatal("completion succeeded without its incident update")
		}
		assertNoAnalysis(t, pool, ctx, job.IncidentID)
		assertJob(t, pool, ctx, ids[0], "running", 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzing")

		if err := faulty.Finish(ctx, job, p.Failure(job.Attempt, "ANALYSIS_PERMANENT", false)); !errors.Is(err, worker.ErrRepository) {
			t.Fatal("failure succeeded without its incident update")
		}
		assertJob(t, pool, ctx, ids[0], "running", 1)
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzing")

		expireJob(t, pool, ctx, job.ID)
		jobExec(t, pool, ctx, "UPDATE analysis_jobs SET attempt_count=$2 WHERE id=$1", ids[0], p.MaxAttempts)
		if err := faulty.Recover(ctx); !errors.Is(err, worker.ErrRepository) {
			t.Fatal("recovery succeeded without its incident update")
		}
		assertIncidentStatus(t, pool, ctx, ids[0], "analyzing")
		if err := repo.Recover(ctx); err != nil {
			t.Fatal("recovery failed")
		}
		assertIncidentStatus(t, pool, ctx, ids[0], "analysis_failed")
	})
}
