package worker

import (
	"context"
	"errors"
	"time"
)

type FakeAnalyzer struct {
	Delay   time.Duration
	Outcome string
}

func (f FakeAnalyzer) Validate() error {
	if f.Delay < 0 || f.Delay > 24*time.Hour {
		return errors.New("WORKER_FAKE_DELAY must be between 0 and 24h")
	}
	switch f.Outcome {
	case "success", "transient", "permanent", "transient-once":
		return nil
	}
	return errors.New("WORKER_FAKE_OUTCOME must be success, transient, permanent, or transient-once")
}
func (f FakeAnalyzer) Analyze(ctx context.Context, job Job) error {
	timer := time.NewTimer(f.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	switch f.Outcome {
	case "success":
		return nil
	case "transient":
		return ErrTransient
	case "transient-once":
		if job.Attempt == 1 {
			return ErrTransient
		}
		return nil
	default:
		return ErrPermanent
	}
}
