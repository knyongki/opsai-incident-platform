//go:build integration

package database

import (
	"errors"
	"testing"

	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

var fakeProvenance = analysis.Provenance{Source: analysis.SourceFake}

func TestAnalysisProvenanceIntegration(t *testing.T) {
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
	claude := analysis.Provenance{Source: analysis.SourceClaude, ModelName: "claude-opus-5-5", PromptVersion: "incident-analysis-v1"}

	t.Run("claude provenance is stored and read back", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		makeAvailable(t, pool, ctx, ids[0])
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID), claude); err != nil {
			t.Fatal("finish with claude provenance failed")
		}
		detail := workerResultDetail(t, pool, ctx, ids[0])
		a := detail.Analysis
		if a == nil || a.Source != "claude" || a.ModelName == nil || *a.ModelName != claude.ModelName ||
			a.PromptVersion == nil || *a.PromptVersion != claude.PromptVersion || detail.Status != "analyzed" {
			t.Fatal("claude provenance did not round-trip through incident detail")
		}
	})

	t.Run("openai provenance is stored and read back", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		makeAvailable(t, pool, ctx, ids[0])
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		p := analysis.Provenance{Source: analysis.SourceOpenAI, ModelName: "example-model", PromptVersion: "incident-analysis-v1"}
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID), p); err != nil {
			t.Fatal("finish with openai provenance failed")
		}
		a := workerResultDetail(t, pool, ctx, ids[0]).Analysis
		if a == nil || a.Source != "openai" || a.ModelName == nil || *a.ModelName != "example-model" ||
			a.PromptVersion == nil || *a.PromptVersion != "incident-analysis-v1" {
			t.Fatal("openai provenance did not round-trip through incident detail")
		}
	})

	t.Run("fake provenance stores no model or prompt", func(t *testing.T) {
		ids := jobFixtures(t, pool, ctx, 1)
		makeAvailable(t, pool, ctx, ids[0])
		job, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal("claim failed")
		}
		if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID), fakeProvenance); err != nil {
			t.Fatal("finish with fake provenance failed")
		}
		a := workerResultDetail(t, pool, ctx, ids[0]).Analysis
		if a == nil || a.Source != "fake" || a.ModelName != nil || a.PromptVersion != nil {
			t.Fatal("fake result gained provenance")
		}
	})

	for _, bad := range []analysis.Provenance{
		{Source: analysis.SourceClaude, ModelName: "claude-opus-5-5"},
		{Source: analysis.SourceFake, ModelName: "claude-opus-5-5"},
		{Source: "real", ModelName: "m", PromptVersion: "v"},
	} {
		t.Run("rejects invalid provenance "+bad.Source, func(t *testing.T) {
			ids := jobFixtures(t, pool, ctx, 1)
			makeAvailable(t, pool, ctx, ids[0])
			job, err := repo.Claim(ctx)
			if err != nil {
				t.Fatal("claim failed")
			}
			if err := repo.FinishWithResult(ctx, job, analysisFixture(job.IncidentID), bad); !errors.Is(err, analysis.ErrInvalid) {
				t.Fatal("invalid provenance was not rejected as invalid")
			}
			assertNoAnalysis(t, pool, ctx, job.IncidentID)
			assertJob(t, pool, ctx, ids[0], "running", 1)
		})
	}
}
