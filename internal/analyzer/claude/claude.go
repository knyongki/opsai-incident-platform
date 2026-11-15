// Package claude implements the worker's Analyzer with the Claude Messages API.
// It returns raw JSON for the worker to validate; it never logs or returns the
// API key, prompts, incident text, or provider error messages.
package claude

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/analyzer/prompt"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

const maxTokens = 16000

// Server-side refusal fallback is sent only to models that accept it.
var fallbackModels = map[string]bool{
	"claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
	"claude-fable-5-1": true, "claude-fable-5-2": true,
}

// Config is validated by internal/config, which owns the accepted effort levels.
type Config struct {
	APIKey string
	Model  string
	Effort string
	// BaseURL overrides the API endpoint; only tests set it.
	BaseURL string
}

type Analyzer struct {
	client anthropic.Client
	model  string
	effort anthropic.BetaOutputConfigEffort
}

func New(cfg Config) (*Analyzer, error) {
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil, errors.New("claude analyzer requires an API key and a model")
	}
	// Environment defaults are skipped so stray ANTHROPIC_* variables cannot change
	// the key, endpoint, or headers. The worker owns retries within its lease.
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &Analyzer{client: anthropic.NewClient(opts...), model: cfg.Model,
		effort: anthropic.BetaOutputConfigEffort(cfg.Effort)}, nil
}

// Check verifies the key and model with a Models API lookup, which generates nothing.
func (a *Analyzer) Check(ctx context.Context) error {
	if _, err := a.client.Models.Get(ctx, a.model, anthropic.ModelGetParams{}); err != nil {
		return classify(ctx, err)
	}
	return nil
}

func (a *Analyzer) Analyze(ctx context.Context, in worker.AnalysisInput) (worker.Output, error) {
	message, err := prompt.UserMessage(in)
	if err != nil {
		return worker.Output{}, worker.ErrPermanent
	}
	params := anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: maxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: prompt.System}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(message))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: a.effort,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: prompt.Schema},
		},
	}
	if fallbackModels[a.model] {
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}
	resp, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return worker.Output{}, classify(ctx, err)
	}
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return worker.Output{}, worker.ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return worker.Output{}, worker.ErrInvalidOutput
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if b, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	if text.Len() == 0 {
		return worker.Output{}, worker.ErrInvalidOutput
	}
	// Record the model that answered, which differs from a.model after a fallback.
	// An unusable value must not discard a paid-for result, so it falls back.
	p := analysis.Provenance{Source: analysis.SourceClaude, ModelName: string(resp.Model), PromptVersion: prompt.Version}
	if p.Validate() != nil {
		p.ModelName = a.model
	}
	return worker.Output{
		JSON:       []byte(text.String()),
		Provenance: p,
		Usage:      worker.Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens},
	}, nil
}

// classify maps SDK errors to worker sentinels without wrapping the original,
// whose text may include provider messages.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return worker.ErrTransient
	}
	switch code := apiErr.StatusCode; {
	// Every request field except the model and effort comes from fixed code, so a
	// 400 means the configuration is unsupported: stop rather than fail each job.
	case code == http.StatusBadRequest, code == http.StatusUnauthorized, code == http.StatusPaymentRequired,
		code == http.StatusForbidden, code == http.StatusNotFound:
		return worker.ErrProviderConfig
	case code == http.StatusRequestTimeout, code == http.StatusConflict,
		code == http.StatusTooManyRequests, code >= 500:
		return worker.ErrTransient
	default:
		return worker.ErrPermanent
	}
}
