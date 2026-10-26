package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type repoStub struct {
	claimErr, recoverErr, finishErr error
	claims, recoveries, finishes    int
	job                             Job
	outcome                         Outcome
}

func (r *repoStub) Claim(ctx context.Context) (Job, error) { r.claims++; return r.job, r.claimErr }
func (r *repoStub) Recover(ctx context.Context) error      { r.recoveries++; return r.recoverErr }
func (r *repoStub) Finish(ctx context.Context, j Job, o Outcome) error {
	r.finishes++
	r.outcome = o
	return r.finishErr
}

type analyzerFunc func(context.Context, Job) error

func (f analyzerFunc) Analyze(c context.Context, j Job) error { return f(c, j) }

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
		{"unknown", errors.New("secret payload"), 1, "failed", "ANALYSIS_PERMANENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &repoStub{job: Job{ID: "job", IncidentID: "incident", Attempt: tc.attempt}}
			var logs bytes.Buffer
			calls := 0
			w, err := New(r, analyzerFunc(func(ctx context.Context, j Job) error {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("no analyzer deadline")
				}
				return tc.err
			}), DefaultPolicy(), slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if err = w.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || r.finishes != 1 || r.outcome.Status != tc.status || r.outcome.Code != tc.code {
				t.Fatalf("unexpected outcome: %+v", r.outcome)
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
		{"completion acknowledgement", nil, nil, ErrRepository, 1},
		{"stale", nil, nil, ErrOwnership, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &repoStub{recoverErr: tc.recoverErr, claimErr: tc.claimErr, finishErr: tc.finishErr, job: Job{Attempt: 1}}
			calls := 0
			w, _ := New(r, analyzerFunc(func(context.Context, Job) error { calls++; return nil }), DefaultPolicy(), slog.Default())
			if w.Step(context.Background()) == nil || calls != tc.calls {
				t.Fatal("failure boundary violated")
			}
			if r.claims > 1 || r.finishes > 1 {
				t.Fatal("immediate duplicate processing")
			}
		})
	}
}
func TestCancellationLeavesLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &repoStub{job: Job{Attempt: 1}}
	w, _ := New(r, analyzerFunc(func(ctx context.Context, _ Job) error { cancel(); return ctx.Err() }), DefaultPolicy(), slog.Default())
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
	r := &repoStub{job: Job{Attempt: 1}}
	w, _ := New(r, analyzerFunc(func(ctx context.Context, _ Job) error { <-ctx.Done(); return nil }), p, slog.Default())
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
	if !errors.Is((FakeAnalyzer{Delay: time.Hour, Outcome: "success"}).Analyze(ctx, Job{}), context.Canceled) {
		t.Fatal("fake ignored cancellation")
	}
	f := FakeAnalyzer{Outcome: "transient-once"}
	if !errors.Is(f.Analyze(context.Background(), Job{Attempt: 1}), ErrTransient) || f.Analyze(context.Background(), Job{Attempt: 2}) != nil {
		t.Fatal("fake retry scenario failed")
	}
	for _, o := range []Outcome{{Status: "bad"}, {Status: "completed", Code: "secret"}, {Status: "failed", Code: "secret"}, {Status: "pending", Code: "ANALYSIS_TRANSIENT", Delay: -1}} {
		if ValidOutcome(o) {
			t.Fatal("unsafe outcome accepted")
		}
	}
}
