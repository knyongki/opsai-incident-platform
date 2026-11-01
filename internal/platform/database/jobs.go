package database

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/platform/database/dbgen"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

type JobRepository struct {
	db     transactionBeginner
	policy worker.Policy
}

func NewJobRepository(db transactionBeginner, policy worker.Policy) (*JobRepository, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &JobRepository{db, policy}, nil
}
func (r *JobRepository) transaction(ctx context.Context, fn func(context.Context, *dbgen.Queries) error) (result error) {
	ctx, cancel := context.WithTimeout(ctx, r.policy.DatabaseTimeout)
	defer cancel()
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return worker.ErrRepository
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), r.policy.DatabaseTimeout)
		defer stop()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			result = worker.ErrRepository
		}
	}()
	if err := fn(ctx, dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return worker.ErrRepository
	}
	return nil
}
func (r *JobRepository) Claim(ctx context.Context) (worker.Job, error) {
	token := pgtype.UUID{Valid: true}
	if _, err := rand.Read(token.Bytes[:]); err != nil {
		return worker.Job{}, worker.ErrRepository
	}
	token.Bytes[6] = (token.Bytes[6] & 15) | 64
	token.Bytes[8] = (token.Bytes[8] & 63) | 128
	var job worker.Job
	err := r.transaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		row, err := q.ClaimAnalysisJob(ctx, dbgen.ClaimAnalysisJobParams{
			ClaimToken: token, LeaseMs: r.policy.Lease.Milliseconds(), MaxAttempts: int32(r.policy.MaxAttempts)})
		if errors.Is(err, pgx.ErrNoRows) {
			return worker.ErrEmpty
		}
		if err != nil {
			return worker.ErrRepository
		}
		job = worker.Job{ID: uuidString(row.ID), IncidentID: uuidString(row.IncidentID),
			Token: uuidString(row.ClaimToken), Attempt: int(row.AttemptCount)}
		return nil
	})
	if err != nil {
		return worker.Job{}, err
	}
	return job, nil
}
func (r *JobRepository) Finish(ctx context.Context, job worker.Job, outcome worker.Outcome) error {
	id, err := uuid(job.ID)
	if err != nil {
		return worker.ErrOwnership
	}
	token, err := uuid(job.Token)
	if err != nil {
		return worker.ErrOwnership
	}
	if !worker.ValidOutcome(outcome) {
		return worker.ErrRepository
	}
	return r.transaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		// Acquire the lock before evaluating expiry: waiting must not extend ownership.
		row, err := q.LockAnalysisJob(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return worker.ErrOwnership
		}
		if err != nil {
			return worker.ErrRepository
		}
		if row.Status != "running" || row.ClaimToken != token {
			return worker.ErrOwnership
		}
		if outcome.Status == "pending" && int(row.AttemptCount) >= r.policy.MaxAttempts {
			outcome.Status, outcome.Delay = "failed", 0
		}
		n, err := q.FinishAnalysisJob(ctx, dbgen.FinishAnalysisJobParams{
			ID: id, ClaimToken: token, NextStatus: outcome.Status, DelayMs: outcome.Delay.Milliseconds(),
			ErrorCode: pgtype.Text{String: outcome.Code, Valid: outcome.Code != ""},
		})
		if err != nil {
			return worker.ErrRepository
		}
		if n != 1 {
			return worker.ErrOwnership
		}
		return nil
	})
}

// FinishWithResult stores a validated analysis and completes its owned job atomically.
func (r *JobRepository) FinishWithResult(ctx context.Context, job worker.Job, result analysis.Result) error {
	id, err := uuid(job.ID)
	if err != nil {
		return worker.ErrOwnership
	}
	token, err := uuid(job.Token)
	if err != nil {
		return worker.ErrOwnership
	}
	analysisID := pgtype.UUID{Valid: true}
	if _, err := rand.Read(analysisID.Bytes[:]); err != nil {
		return worker.ErrRepository
	}
	analysisID.Bytes[6] = (analysisID.Bytes[6] & 15) | 64
	analysisID.Bytes[8] = (analysisID.Bytes[8] & 63) | 128
	return r.transaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		row, err := q.LockAnalysisJob(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return worker.ErrOwnership
		}
		if err != nil {
			return worker.ErrRepository
		}
		if row.Status != "running" || row.ClaimToken != token {
			return worker.ErrOwnership
		}
		incidentID := uuidString(row.IncidentID)
		if err := analysis.Validate(result, incidentID); err != nil {
			return analysis.ErrInvalid
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return analysis.ErrInvalid
		}
		if err := q.InsertIncidentAnalysis(ctx, dbgen.InsertIncidentAnalysisParams{
			ID: analysisID, IncidentID: row.IncidentID, SchemaVersion: analysis.SchemaVersion,
			Source: "fake", Result: encoded,
		}); err != nil {
			return worker.ErrRepository
		}
		n, err := q.FinishAnalysisJob(ctx, dbgen.FinishAnalysisJobParams{
			ID: id, ClaimToken: token, NextStatus: "completed",
		})
		if err != nil {
			return worker.ErrRepository
		}
		if n != 1 {
			return worker.ErrOwnership
		}
		return nil
	})
}
func (r *JobRepository) Recover(ctx context.Context) error {
	return r.transaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		rows, err := q.LockRecoverableAnalysisJobs(ctx, int32(r.policy.MaxAttempts))
		if err != nil {
			return worker.ErrRepository
		}
		for _, row := range rows {
			code := "LEASE_EXPIRED"
			if row.Status == "pending" {
				code = "ATTEMPTS_EXHAUSTED"
			}
			outcome := r.policy.Failure(int(row.AttemptCount), code, true)
			n, err := q.RecoverAnalysisJob(ctx, dbgen.RecoverAnalysisJobParams{
				ID: row.ID, ClaimToken: row.ClaimToken, MaxAttempts: int32(r.policy.MaxAttempts),
				NextStatus: outcome.Status, DelayMs: outcome.Delay.Milliseconds(), ErrorCode: code,
			})
			if err != nil || n != 1 {
				return worker.ErrRepository
			}
		}
		return nil
	})
}
