package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/analysis"
)

type repoStub struct {
	claimErr, recoverErr, loadErr, finishErr, resultErr error
	claims, recoveries, loads, finishes, resultFinishes int
	job                                                 Job
	input                                               AnalysisInput
	outcome                                             Outcome
	result                                              analysis.Result
}

func (r *repoStub) Claim(ctx context.Context) (Job, error) { r.claims++; return r.job, r.claimErr }
func (r *repoStub) Recover(ctx context.Context) error      { r.recoveries++; return r.recoverErr }
func (r *repoStub) LoadAnalysisInput(_ context.Context, job Job) (AnalysisInput, error) {
	r.loads++
	if r.loadErr != nil {
		return AnalysisInput{}, r.loadErr
	}
	if r.input.IncidentID == "" {
		r.input = AnalysisInput{IncidentID: job.IncidentID, Attempt: job.Attempt}
	}
	return r.input, nil
}
func (r *repoStub) Finish(ctx context.Context, j Job, o Outcome) error {
	r.finishes++
	r.outcome = o
	return r.finishErr
}
func (r *repoStub) FinishWithResult(_ context.Context, _ Job, result analysis.Result) error {
	r.resultFinishes++
	r.result = result
	r.outcome = Outcome{Status: "completed"}
	return r.resultErr
}

type analyzerFunc func(context.Context, AnalysisInput) ([]byte, error)

func (f analyzerFunc) Analyze(c context.Context, input AnalysisInput) ([]byte, error) {
	return f(c, input)
}

const testIncidentID = "bac4a3b9-d98f-46cc-a72d-72daba2b2715"

func testJob(attempt int) Job {
	return Job{ID: "job", IncidentID: testIncidentID, Token: "claim-token", Attempt: attempt}
}

func validOutput(incidentID string) []byte {
	data, _ := json.Marshal(analysis.Result{
		Summary: "Synthetic summary", PrimarySuspectedCause: "No diagnosis was performed",
		ContributingFactors: []string{}, ObservedConditions: []string{"Synthetic input"},
		RecommendedActions: []analysis.Action{}, SafetyNotes: []string{"Test output only"},
		Confidence: 0, Evidence: []analysis.Evidence{{SourceID: incidentID, Claim: "Synthetic incident record"}},
	})
	return data
}

func TestStepOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		attempt      int
		status, code string
	}{
		{"success", nil, 1, "completed", ""},
		{"retry", ErrTransient, 1, "pending", "ANALYSIS_TRANSIENT"},
		{"exhausted", ErrTransient, 3, "failed", "ANALYSIS_TRANSIENT"},
		{"timeout", context.DeadlineExceeded, 1, "pending", "ANALYSIS_TIMEOUT"},
		{"permanent", ErrPermanent, 1, "failed", "ANALYSIS_PERMANENT"},
		{"invalid output", nil, 1, "failed", "ANALYSIS_INVALID_OUTPUT"},
		{"unknown", errors.New("secret payload"), 1, "failed", "ANALYSIS_PERMANENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &repoStub{job: testJob(tc.attempt)}
			var logs bytes.Buffer
			calls := 0
			w, err := New(r, analyzerFunc(func(ctx context.Context, input AnalysisInput) ([]byte, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("no analyzer deadline")
				}
				if tc.err != nil {
					return []byte("secret payload"), tc.err
				}
				if tc.name == "invalid output" {
					return []byte("secret payload"), nil
				}
				return validOutput(input.IncidentID), nil
			}), DefaultPolicy(), slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if err = w.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || r.loads != 1 || r.outcome.Status != tc.status || r.outcome.Code != tc.code {
				t.Fatalf("unexpected outcome: %+v", r.outcome)
			}
			if tc.status == "completed" && (r.resultFinishes != 1 || r.finishes != 0) || tc.status != "completed" && (r.finishes != 1 || r.resultFinishes != 0) {
				t.Fatal("success and failure used the wrong repository operation")
			}
			if tc.status == "pending" && r.outcome.Delay != 2*time.Second {
				t.Fatal("retry backoff missing")
			}
			if strings.Contains(logs.String(), "secret payload") {
				t.Fatal("unsafe log")
			}
		})
	}
}
func TestStepFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		recoverErr, claimErr, finishErr error
		calls                           int
	}{
		{"recovery", ErrRepository, nil, nil, 0},
		{"claim", nil, ErrRepository, nil, 0},
		{"empty", nil, ErrEmpty, nil, 0},
		{"input loading", nil, nil, nil, 0},
		{"completion acknowledgement", nil, nil, ErrRepository, 1},
		{"stale", nil, nil, ErrOwnership, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &repoStub{recoverErr: tc.recoverErr, claimErr: tc.claimErr, finishErr: tc.finishErr, job: testJob(1)}
			if tc.name == "completion acknowledgement" || tc.name == "stale" {
				r.resultErr = tc.finishErr
				r.finishErr = nil
			}
			if tc.name == "input loading" {
				r.loadErr = ErrRepository
			}
			calls := 0
			w, _ := New(r, analyzerFunc(func(_ context.Context, input AnalysisInput) ([]byte, error) {
				calls++
				return validOutput(input.IncidentID), nil
			}), DefaultPolicy(), slog.Default())
			if w.Step(context.Background()) == nil || calls != tc.calls {
				t.Fatal("failure boundary violated")
			}
			if r.claims > 1 || r.finishes > 1 || r.resultFinishes > 1 {
				t.Fatal("immediate duplicate processing")
			}
		})
	}
}

func TestStepReleasesJobWhenInputLoadFails(t *testing.T) {
	for _, tc := range []struct {
		name               string
		loadErr, finishErr error
		finishes           int
	}{
		{"storage failure releases claim", ErrRepository, nil, 1},
		{"release failure awaits recovery", ErrRepository, ErrRepository, 1},
		{"lost ownership is not finished", ErrOwnership, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &repoStub{job: testJob(1), loadErr: tc.loadErr, finishErr: tc.finishErr}
			w, _ := New(r, analyzerFunc(func(context.Context, AnalysisInput) ([]byte, error) {
				t.Fatal("analyzer ran without input")
				return nil, nil
			}), DefaultPolicy(), slog.Default())
			if err := w.Step(context.Background()); !errors.Is(err, tc.loadErr) {
				t.Fatalf("got %v, want %v", err, tc.loadErr)
			}
			if r.finishes != tc.finishes {
				t.Fatalf("finishes = %d, want %d", r.finishes, tc.finishes)
			}
			if tc.finishes == 1 && (r.outcome.Status != "pending" || r.outcome.Code != "ANALYSIS_INPUT_UNAVAILABLE" || r.outcome.Delay != 2*time.Second) {
				t.Fatalf("unexpected release outcome: %+v", r.outcome)
			}
		})
	}
}

func TestStepRecordsResultRejectedByRepository(t *testing.T) {
	r := &repoStub{job: testJob(1), resultErr: analysis.ErrInvalid}
	w, _ := New(r, analyzerFunc(func(_ context.Context, input AnalysisInput) ([]byte, error) {
		return validOutput(input.IncidentID), nil
	}), DefaultPolicy(), slog.Default())
	if err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.resultFinishes != 1 || r.finishes != 1 || r.outcome.Status != "failed" || r.outcome.Code != "ANALYSIS_INVALID_OUTPUT" {
		t.Fatalf("rejected result not recorded as invalid output: %+v", r.outcome)
	}
}

func TestPolicyLeaseCoversThreeDatabaseCalls(t *testing.T) {
	p := DefaultPolicy()
	p.Lease = p.AttemptTimeout + 3*p.DatabaseTimeout
	if p.Validate() == nil {
		t.Fatal("lease without margin for claim, load, and completion accepted")
	}
	p.Lease += time.Millisecond
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStepPassesClaimedIncidentInput(t *testing.T) {
	want := AnalysisInput{IncidentID: testIncidentID, Attempt: 2, Title: "Synthetic title",
		Description: "Synthetic description", Severity: "high", RawLogs: []string{"synthetic log"}}
	r := &repoStub{job: testJob(2), input: want}
	w, err := New(r, analyzerFunc(func(_ context.Context, input AnalysisInput) ([]byte, error) {
		if input.IncidentID != want.IncidentID || input.Attempt != want.Attempt || input.Title != want.Title ||
			input.Description != want.Description || input.Severity != want.Severity || len(input.RawLogs) != 1 || input.RawLogs[0] != want.RawLogs[0] {
			t.Fatal("worker did not forward the loaded incident input")
		}
		return validOutput(input.IncidentID), nil
	}), DefaultPolicy(), slog.Default())
	if err != nil || w.Step(context.Background()) != nil {
		t.Fatal("worker failed to process loaded input")
	}
}

func TestCancellationLeavesLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &repoStub{job: testJob(1)}
	w, _ := New(r, analyzerFunc(func(ctx context.Context, _ AnalysisInput) ([]byte, error) {
		cancel()
		return nil, ctx.Err()
	}), DefaultPolicy(), slog.Default())
	if !errors.Is(w.Step(ctx), context.Canceled) || r.finishes != 0 {
		t.Fatal("canceled job was finalized")
	}
	claims := r.claims
	w.Run(ctx)
	if r.claims != claims {
		t.Fatal("claimed after shutdown")
	}
}
func TestAnalyzerDeadlineAndLateSuccess(t *testing.T) {
	p := DefaultPolicy()
	p.AttemptTimeout = time.Millisecond
	r := &repoStub{job: testJob(1)}
	w, _ := New(r, analyzerFunc(func(ctx context.Context, input AnalysisInput) ([]byte, error) {
		<-ctx.Done()
		return validOutput(input.IncidentID), nil
	}), p, slog.Default())
	if err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.outcome.Code != "ANALYSIS_TIMEOUT" {
		t.Fatal("late success accepted")
	}
}
func TestRunCancellationDuringPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &repoStub{claimErr: ErrEmpty}
	w, _ := New(r, FakeAnalyzer{}, DefaultPolicy(), slog.Default())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poll ignored cancellation")
	}
}
func TestPolicyAndFake(t *testing.T) {
	p := DefaultPolicy()
	for i, want := range []time.Duration{2, 4, 8, 16, 32, 60, 60} {
		if p.Backoff(i+1) != want*time.Second {
			t.Fatal("bad exponential cap")
		}
	}
	if p.Backoff(1000000) != time.Minute {
		t.Fatal("backoff overflow")
	}
	for _, mutate := range []func(*Policy){
		func(p *Policy) { p.MaxAttempts = 0 }, func(p *Policy) { p.MaxAttempts = 101 },
		func(p *Policy) { p.Lease = p.AttemptTimeout }, func(p *Policy) { p.PollInterval = 0 },
		func(p *Policy) { p.RetryCap = time.Millisecond },
	} {
		bad := p
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (FakeAnalyzer{Delay: time.Hour, Outcome: "success"}).Analyze(ctx, AnalysisInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal("fake ignored cancellation")
	}
	f := FakeAnalyzer{Outcome: "transient-once"}
	if _, err := f.Analyze(context.Background(), AnalysisInput{Attempt: 1}); !errors.Is(err, ErrTransient) {
		t.Fatal("fake retry scenario failed")
	}
	if _, err := f.Analyze(context.Background(), AnalysisInput{Attempt: 2}); err != nil {
		t.Fatal("fake retry scenario failed")
	}
	if err := (FakeAnalyzer{Outcome: "invalid-output"}).Validate(); err != nil {
		t.Fatal("invalid output mode rejected")
	}
	if _, err := (FakeAnalyzer{Outcome: "invalid-output"}).Analyze(context.Background(), AnalysisInput{}); err != nil {
		t.Fatal("invalid-output scenario should return bytes for validation")
	}
	for _, o := range []Outcome{{Status: "bad"}, {Status: "completed", Code: "secret"}, {Status: "failed", Code: "secret"}, {Status: "pending", Code: "ANALYSIS_TRANSIENT", Delay: -1}} {
		if ValidOutcome(o) {
			t.Fatal("unsafe outcome accepted")
		}
	}
}
