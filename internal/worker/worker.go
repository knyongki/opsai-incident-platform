// Package worker processes durable jobs without depending on a transport or database driver.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/analysis"
)

var (
	ErrEmpty      = errors.New("no available job")
	ErrOwnership  = errors.New("job ownership lost")
	ErrRepository = errors.New("job storage operation failed")
	ErrTransient  = errors.New("transient analyzer failure")
	ErrPermanent  = errors.New("permanent analyzer failure")
	// ErrRefused means the model declined the request; retrying will not help.
	ErrRefused = errors.New("analyzer refused the request")
	// ErrInvalidOutput means the analyzer produced no usable result, for example
	// a response cut off at the token limit.
	ErrInvalidOutput = errors.New("analyzer output unusable")
	// ErrProviderConfig means the provider rejected the worker's credentials or
	// access. The job is released and the worker stops, so a bad key cannot fail
	// the whole queue.
	ErrProviderConfig = errors.New("analyzer provider configuration rejected")
)

type Job struct {
	ID, IncidentID, Token string
	Attempt               int
}
type AnalysisInput struct {
	IncidentID     string
	Attempt        int
	Title          string
	Description    string
	AlarmCode      *string
	Severity       string
	RawLogs        []string
	OccurredAt     *time.Time
	DeviceName     string
	DeviceType     string
	DeviceLocation *string
}

// Output is an analyzer's raw JSON result plus trusted metadata stored with it.
type Output struct {
	JSON       []byte
	Provenance analysis.Provenance
	Usage      Usage
}

// Usage holds provider token counts for logging; zero for the fake analyzer.
type Usage struct {
	InputTokens, OutputTokens int64
}

type Repository interface {
	Claim(context.Context) (Job, error)
	LoadAnalysisInput(context.Context, Job) (AnalysisInput, error)
	Finish(context.Context, Job, Outcome) error
	// Release returns an owned job to the queue and gives its attempt back, for
	// failures caused by worker configuration rather than by the job.
	Release(ctx context.Context, job Job, code string) error
	FinishWithResult(context.Context, Job, analysis.Result, analysis.Provenance) error
	Recover(context.Context) error
}
type Analyzer interface {
	Analyze(context.Context, AnalysisInput) (Output, error)
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
	// The lease starts at claim and must outlast claim, input load, analysis, and completion.
	if p.Lease <= p.AttemptTimeout+3*p.DatabaseTimeout {
		return errors.New("JOB_LEASE must exceed attempt timeout plus three database timeouts")
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
	case errors.Is(err, ErrRefused):
		return p.Failure(attempt, "ANALYSIS_REFUSED", false)
	case errors.Is(err, ErrInvalidOutput):
		return p.Failure(attempt, "ANALYSIS_INVALID_OUTPUT", false)
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
	case "ANALYSIS_TIMEOUT", "ANALYSIS_TRANSIENT", "ANALYSIS_PERMANENT", "ANALYSIS_INVALID_OUTPUT",
		"ANALYSIS_INPUT_UNAVAILABLE", "ANALYSIS_REFUSED", "ANALYSIS_PROVIDER_CONFIG":
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

	dbctx, cancel = context.WithTimeout(ctx, w.policy.DatabaseTimeout)
	input, err := w.repo.LoadAnalysisInput(dbctx, job)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrRepository) {
		// Release the claim as a retryable failure instead of holding it until lease expiry.
		// If this also fails, recovery still reclaims the job after its lease.
		dbctx, cancel = context.WithTimeout(ctx, w.policy.DatabaseTimeout)
		releaseErr := w.repo.Finish(dbctx, job, w.policy.Failure(job.Attempt, "ANALYSIS_INPUT_UNAVAILABLE", true))
		cancel()
		if releaseErr != nil {
			w.logger.Warn("claimed job release failed; awaiting lease recovery", "job_id", job.ID,
				"error_code", "JOB_RELEASE_FAILED")
		}
		return err
	}
	if err != nil {
		return err
	}
	started := time.Now()
	w.logger.Info("analysis started", "job_id", job.ID, "incident_id", input.IncidentID, "attempt", job.Attempt)
	actx, stop := context.WithTimeout(ctx, w.policy.AttemptTimeout)
	output, analysisErr := w.analyzer.Analyze(actx, input)
	// A late success cannot turn an expired attempt into success.
	if actx.Err() != nil {
		analysisErr = actx.Err()
	}
	stop()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(analysisErr, ErrProviderConfig) {
		dbctx, cancel := context.WithTimeout(ctx, w.policy.DatabaseTimeout)
		releaseErr := w.repo.Release(dbctx, job, "ANALYSIS_PROVIDER_CONFIG")
		cancel()
		w.logger.Error("analyzer provider rejected the worker configuration; stopping", "job_id", job.ID,
			"error_code", "ANALYSIS_PROVIDER_CONFIG")
		if releaseErr != nil {
			w.logger.Warn("claimed job release failed; awaiting lease recovery", "job_id", job.ID,
				"error_code", "JOB_RELEASE_FAILED")
		}
		return ErrProviderConfig
	}
	var result *analysis.Result
	var outcome Outcome
	if analysisErr != nil {
		outcome = w.policy.Result(job.Attempt, analysisErr)
	} else {
		validated, err := analysis.Decode(output.JSON, input.IncidentID)
		if err != nil {
			outcome = Outcome{Status: "failed", Code: "ANALYSIS_INVALID_OUTPUT"}
		} else {
			result = &validated
			outcome = Outcome{Status: "completed"}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	dbctx, cancel = context.WithTimeout(ctx, w.policy.DatabaseTimeout)
	defer cancel()
	if result != nil {
		err = w.repo.FinishWithResult(dbctx, job, *result, output.Provenance)
		// The rolled-back transaction rejected the result; record it as invalid output.
		// Reusing dbctx keeps both calls within one database timeout of the lease budget.
		if errors.Is(err, analysis.ErrInvalid) {
			outcome = Outcome{Status: "failed", Code: "ANALYSIS_INVALID_OUTPUT"}
			err = w.repo.Finish(dbctx, job, outcome)
		}
	} else {
		err = w.repo.Finish(dbctx, job, outcome)
	}
	if err != nil {
		return err
	}
	w.logger.Info("analysis finished", "job_id", job.ID, "incident_id", input.IncidentID,
		"attempt", job.Attempt, "duration_ms", time.Since(started).Milliseconds(),
		"status", outcome.Status, "error_code", outcome.Code,
		"source", output.Provenance.Source, "model_name", output.Provenance.ModelName,
		"prompt_version", output.Provenance.PromptVersion,
		"input_tokens", output.Usage.InputTokens, "output_tokens", output.Usage.OutputTokens)
	return nil
}

// Run processes jobs until ctx is canceled (nil) or the provider rejects the
// worker's configuration (ErrProviderConfig), which needs operator action.
func (w *Worker) Run(ctx context.Context) error {
	failures := 0
	for ctx.Err() == nil {
		err := w.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrProviderConfig) {
			return err
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
			return nil
		case <-timer.C:
		}
	}
	return nil
}
