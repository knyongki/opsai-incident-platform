package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/analysis"
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
	case "success", "transient", "permanent", "transient-once", "invalid-output":
		return nil
	}
	return errors.New("WORKER_FAKE_OUTCOME must be success, transient, permanent, transient-once, or invalid-output")
}
func (f FakeAnalyzer) Analyze(ctx context.Context, input AnalysisInput) (Output, error) {
	data, err := f.analyze(ctx, input)
	return Output{JSON: data, Provenance: analysis.Provenance{Source: analysis.SourceFake}}, err
}

func (f FakeAnalyzer) analyze(ctx context.Context, input AnalysisInput) ([]byte, error) {
	timer := time.NewTimer(f.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	switch f.Outcome {
	case "success", "transient-once":
		if f.Outcome == "transient-once" && input.Attempt == 1 {
			return nil, ErrTransient
		}
		return json.Marshal(analysis.Result{
			Summary:               "Synthetic analysis completed; no diagnosis was performed.",
			PrimarySuspectedCause: "No suspected cause was determined by the fake analyzer.",
			ContributingFactors:   []string{},
			ObservedConditions:    []string{"The incident record was received by the fake analyzer."},
			RecommendedActions:    []analysis.Action{},
			SafetyNotes:           []string{"Synthetic test output only; do not use for equipment operation."},
			Confidence:            0,
			Evidence:              []analysis.Evidence{{SourceID: input.IncidentID, Claim: "References only the incident record supplied to the fake analyzer."}},
		})
	case "invalid-output":
		return []byte(`{"summary":`), nil
	case "transient":
		return nil, ErrTransient
	default:
		return nil, ErrPermanent
	}
}
