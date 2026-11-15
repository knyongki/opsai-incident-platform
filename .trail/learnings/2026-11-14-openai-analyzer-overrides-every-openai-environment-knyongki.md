---
title: OpenAI analyzer overrides every OPENAI environment default because the SDK cannot turn them off
author: Knyongki
date: 2026-11-14
branch: main
cost: { minutes: 14, tokens: 96332 }
id: l-b4689443
scope: repo
status: active
src: agent
touches:
  - db/migrations/00006_allow_openai_analyses.sql
  - internal/analyzer/openai/openai_test.go
  - internal/analyzer/openai/openai.go
---

## Decided
- The OpenAI Go SDK always reads OPENAI_API_KEY, OPENAI_BASE_URL, OPENAI_ORG_ID, OPENAI_PROJECT_ID, OPENAI_ADMIN_KEY, and OPENAI_CUSTOM_HEADERS, and has no public option to skip them (unlike the Anthropic SDK). The adapter therefore sets the key and base URL explicitly, clears the admin key, and deletes the organization and project headers. A test sets those variables and proves the request is unchanged.
- OPENAI_CUSTOM_HEADERS cannot be removed generically; environment variables are trusted configuration here, so that remaining gap is accepted.
- A 429 with code insufficient_quota is a billing problem, not a rate limit: it releases the job and stops the worker instead of retrying.
- A 400 about the model or reasoning effort (unsupported_parameter, unsupported_value, model_not_found, param "model" or "reasoning.*") is a configuration error, so one wrong setting cannot fail every queued incident.
- There is no default OpenAI model; WORKER_AI_MODEL is required for openai.

## Watch out
- Both providers must keep sending the shared prompt package; change it only with a new Version and pinned hash.

In the code: internal/analyzer/openai/openai.go (New, classify), internal/analyzer/openai/openai_test.go (TestAnalyzeIgnoresOpenAIEnvironment), internal/analyzer/prompt/prompt.go.
