---
title: Claude analyzer lets the worker own retries and ignores ANTHROPIC environment variables
author: Knyongki
date: 2026-11-14
branch: main
cost: { minutes: 23, tokens: 197443 }
id: l-b7fa3cbf
scope: repo
status: active
src: agent
touches:
  - AGENTS.md
  - ".env.example"
  - README.md
  - api/postman/OpsAI API/Incidents - Manual/Get Created Incident.request.yaml
  - api/postman/OpsAI API/Incidents - Manual/List Incidents With Filters.request.yaml
  - api/postman/OpsAI API/Incidents - Worker Result/Poll Worker Result.request.yaml
  - cmd/worker/main.go
  - db/queries/jobs.sql
  - docs/API-Reference.md
  - docs/Local-Automation-Setup.md
---

## Decided
- The Claude client is built with SDK retries off, so the worker's bounded retry policy and job lease stay the only retry mechanism. SDK retries inside one attempt would eat the lease budget and hide failures from the job's attempt count.
- The client skips the SDK's environment defaults, so stray ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL, or ANTHROPIC_CUSTOM_HEADERS variables cannot change the key, endpoint, or headers. Only WORKER_AI_* settings apply.
- 401, 402, 403, and 404 release the job and stop the worker instead of failing it, so a bad key or model cannot burn through the queue. A Models API lookup at startup catches most of these before any job is claimed.
- Server-side refusal fallback is sent only to models that accept it; the stored model_name is the model that actually answered.

## Watch out
- SDK v2 sends the API key as "Authorization: Bearer <key>", not "x-api-key". Tests against a fake server must check the Authorization header.
- Changing the system prompt, user template, or output schema changes the pinned hash: bump PromptVersion, then update the hash in the test.
- In tests, re-encoding a message with json.Marshal escapes < and >, so check the decoded text, not the marshalled JSON, for the <incident_data> delimiters.

In the code: internal/analyzer/claude/claude.go (New, classify), internal/analyzer/claude/prompt.go (PromptVersion), internal/analyzer/claude/claude_test.go (TestPromptVersionPinned).
