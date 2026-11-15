// Package openai implements the worker's Analyzer with the OpenAI Responses API.
// It sends the shared prompt and returns raw JSON for the worker to validate; it
// never logs or returns the API key, prompts, incident text, or provider messages.
package openai

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/knyongki/opsai-incident-platform/internal/analysis"
	"github.com/knyongki/opsai-incident-platform/internal/analyzer/prompt"
	"github.com/knyongki/opsai-incident-platform/internal/worker"
)

const (
	maxOutputTokens = 16000
	productionURL   = "https://api.openai.com/v1/"
	schemaName      = "incident_analysis"
)

// Config is validated by internal/config, which owns the accepted effort levels.
type Config struct {
	APIKey string
	Model  string
	Effort string
	// BaseURL overrides the API endpoint; only tests set it.
	BaseURL string
}

type Analyzer struct {
	client openai.Client
	model  string
	effort shared.ReasoningEffort
}

func New(cfg Config) (*Analyzer, error) {
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil, errors.New("openai analyzer requires an API key and a model")
	}
	base := cfg.BaseURL
	if base == "" {
		base = productionURL
	}
	// The SDK always reads OPENAI_* defaults; these options override every one that
	// could change the key, endpoint, or account headers. The worker owns retries.
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(base),
		option.WithAdminAPIKey(""),
		option.WithHeaderDel("OpenAI-Organization"),
		option.WithHeaderDel("OpenAI-Project"),
		option.WithMaxRetries(0),
	)
	return &Analyzer{client: client, model: cfg.Model, effort: shared.ReasoningEffort(cfg.Effort)}, nil
}

// Check verifies the key and model with a Models API lookup, which generates nothing.
func (a *Analyzer) Check(ctx context.Context) error {
	if _, err := a.client.Models.Get(ctx, a.model); err != nil {
		return classify(ctx, err)
	}
	return nil
}

func (a *Analyzer) Analyze(ctx context.Context, in worker.AnalysisInput) (worker.Output, error) {
	message, err := prompt.UserMessage(in)
	if err != nil {
		return worker.Output{}, worker.ErrPermanent
	}
	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:           a.model,
		Instructions:    openai.String(prompt.System),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(message)},
		MaxOutputTokens: openai.Int(maxOutputTokens),
		// Incident data is not kept for later retrieval on OpenAI's side.
		Store:     openai.Bool(false),
		Reasoning: shared.ReasoningParam{Effort: a.effort},
		Text: responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigUnionParam{
			OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
				Name: schemaName, Schema: prompt.Schema, Strict: openai.Bool(true),
			},
		}},
	})
	if err != nil {
		return worker.Output{}, classify(ctx, err)
	}
	switch resp.Status {
	case responses.ResponseStatusIncomplete:
		return worker.Output{}, worker.ErrInvalidOutput
	case responses.ResponseStatusFailed:
		return worker.Output{}, worker.ErrTransient
	}
	var text strings.Builder
	for _, item := range resp.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			switch part.Type {
			case "refusal":
				return worker.Output{}, worker.ErrRefused
			case "output_text":
				text.WriteString(part.Text)
			}
		}
	}
	if text.Len() == 0 {
		return worker.Output{}, worker.ErrInvalidOutput
	}
	// Record the model that answered, which may be a dated snapshot of a.model.
	// An unusable value must not discard a paid-for result, so it falls back.
	p := analysis.Provenance{Source: analysis.SourceOpenAI, ModelName: string(resp.Model), PromptVersion: prompt.Version}
	if p.Validate() != nil {
		p.ModelName = a.model
	}
	return worker.Output{
		JSON:       []byte(text.String()),
		Provenance: p,
		Usage:      worker.Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens},
	}, nil
}

// Request errors caused by the worker's model or effort settings stop the worker
// instead of failing every queued job.
var configCodes = map[string]bool{"unsupported_parameter": true, "unsupported_value": true, "model_not_found": true}

// classify maps SDK errors to worker sentinels without wrapping the original,
// whose text may include provider messages.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return worker.ErrTransient
	}
	switch code := apiErr.StatusCode; {
	case code == http.StatusTooManyRequests:
		if apiErr.Code == "insufficient_quota" {
			return worker.ErrProviderConfig
		}
		return worker.ErrTransient
	case code == http.StatusUnauthorized, code == http.StatusPaymentRequired,
		code == http.StatusForbidden, code == http.StatusNotFound:
		return worker.ErrProviderConfig
	case code == http.StatusBadRequest, code == http.StatusUnprocessableEntity:
		if configCodes[apiErr.Code] || apiErr.Param == "model" || strings.HasPrefix(apiErr.Param, "reasoning") {
			return worker.ErrProviderConfig
		}
		return worker.ErrPermanent
	case code == http.StatusRequestTimeout, code == http.StatusConflict, code >= 500:
		return worker.ErrTransient
	default:
		return worker.ErrPermanent
	}
}
