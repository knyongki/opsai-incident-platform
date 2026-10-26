// Package worker processes durable jobs without depending on a transport or database driver.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var (
	ErrEmpty      = errors.New("no available job")
	ErrOwnership  = errors.New("job ownership lost")
	ErrRepository = errors.New("job storage operation failed")
	ErrTransient  = errors.New("transient analyzer failure")
	ErrPermanent  = errors.New("permanent analyzer failure")
)

type Job struct {
	ID, IncidentID, Token string
	Attempt               int
}
type Repository interface {
	Claim(context.Context) (Job, error)
	Finish(context.Context, Job, Outcome) error
	Recover(context.Context) error
}
type Analyzer interface {
	Analyze(context.Context, Job) error
}

type Outcome struct {
	Status, Code string
	Delay        time.Duration
}

type Policy struct {
	PollInterval, AttemptTimeout, Lease, DatabaseTimeout time.Duration
	RetryBase, RetryCap, ShutdownTimeout                 time.Duration
	MaxAttempts                                          int
}

func DefaultPolicy() Policy {
	return Policy{time.Second, 30 * time.Second, 60 * time.Second, 5 * time.Second,
		2 * time.Second, 60 * time.Second, 10 * time.Second, 3}
}
func (p Policy) Validate() error {
	for _, d := range []time.Duration{p.PollInterval, p.AttemptTimeout, p.Lease, p.DatabaseTimeout,
		p.RetryBase, p.RetryCap, p.ShutdownTimeout} {
		if d < time.Millisecond || d > 24*time.Hour {
			return errors.New("worker durations must be between 1ms and 24h")
		}
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > 100 {
		return errors.New("JOB_MAX_ATTEMPTS must be between 1 and 100")
	}
	if p.RetryCap < p.RetryBase {
		return errors.New("JOB_RETRY_CAP must be at least JOB_RETRY_BASE")
	}
	if p.Lease <= p.AttemptTimeout+2*p.DatabaseTimeout {
		return errors.New("JOB_LEASE must exceed attempt timeout plus two database timeouts")
	}
	return nil
}
func (p Policy) Backoff(attempt int) time.Duration {
	delay := p.RetryBase
	for n := 1; n < attempt && delay < p.RetryCap; n++ {
		if delay > p.RetryCap/2 {
			return p.RetryCap
		}
		delay *= 2
	}
	return min(delay, p.RetryCap)
}
func (p Policy) Failure(attempt int, code string, retry bool) Outcome {
	if retry && attempt < p.MaxAttempts {
		return Outcome{Status: "pending", Code: code, Delay: p.Backoff(attempt)}
	}
	return Outcome{Status: "failed", Code: code}
}
func (p Policy) Result(attempt int, err error) Outcome {
	switch {
	case err == nil:
		return Outcome{Status: "completed"}
	case errors.Is(err, context.DeadlineExceeded):
		return p.Failure(attempt, "ANALYSIS_TIMEOUT", true)
	case errors.Is(err, ErrTransient):
		return p.Failure(attempt, "ANALYSIS_TRANSIENT", true)
	default:
		return p.Failure(attempt, "ANALYSIS_PERMANENT", false)
	}
}
func ValidOutcome(o Outcome) bool {
	if o.Status == "completed" {
		return o.Code == "" && o.Delay == 0
	}
	if o.Status != "pending" && o.Status != "failed" {
		return false
	}
	if o.Delay < 0 || o.Delay > 24*time.Hour || (o.Status == "failed" && o.Delay != 0) {
		return false
	}
	switch o.Code {
	case "ANALYSIS_TIMEOUT", "ANALYSIS_TRANSIENT", "ANALYSIS_PERMANENT":
		return true
	}
	return false
}

type Worker struct {
	repo     Repository
	analyzer Analyzer
	policy   Policy
	logger   *slog.Logger
}

func New(repo Repository, analyzer Analyzer, policy Policy, logger *slog.Logger) (*Worker, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if repo == nil || analyzer == nil || logger == nil {
		return nil, errors.New("worker dependencies are required")
	}
	return &Worker{repo, analyzer, policy, logger}, nil
}

// Step processes at most one job. Analyzer work never holds a database transaction.
func (w *Worker) Step(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dbctx, cancel := context.WithTimeout(ctx, w.policy.DatabaseTimeout)
	err := w.repo.Recover(dbctx)
	cancel()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dbctx, cancel = context.WithTimeout(ctx, w.policy.DatabaseTimeout)
	job, err := w.repo.Claim(dbctx)
	cancel()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	started := time.Now()
	w.logger.Info("fake analysis started", "job_id", job.ID, "incident_id", job.IncidentID, "attempt", job.Attempt)
	actx, stop := context.WithTimeout(ctx, w.policy.AttemptTimeout)
	err = w.analyzer.Analyze(actx, job)
	// A late success cannot turn an expired attempt into success.
	if actx.Err() != nil {
		err = actx.Err()
	}
	stop()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	outcome := w.policy.Result(job.Attempt, err)
	dbctx, cancel = context.WithTimeout(ctx, w.policy.DatabaseTimeout)
	defer cancel()
	if err := w.repo.Finish(dbctx, job, outcome); err != nil {
		return err
	}
	w.logger.Info("fake analysis finished", "job_id", job.ID, "incident_id", job.IncidentID,
		"attempt", job.Attempt, "duration_ms", time.Since(started).Milliseconds(),
		"status", outcome.Status, "error_code", outcome.Code)
	return nil
}
func (w *Worker) Run(ctx context.Context) {
	failures := 0
	for ctx.Err() == nil {
		err := w.Step(ctx)
		if ctx.Err() != nil {
			return
		}
		delay := w.policy.PollInterval
		switch {
		case err == nil:
			failures = 0
			continue
		case errors.Is(err, ErrEmpty):
			failures = 0
		case errors.Is(err, ErrOwnership):
			failures = 0
			w.logger.Warn("job ownership lost", "error_code", "OWNERSHIP_LOST")
		default:
			failures = min(failures+1, 100)
			delay = max(delay, w.policy.Backoff(failures))
			w.logger.Error("worker storage operation failed", "error_code", "JOB_STORAGE")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
