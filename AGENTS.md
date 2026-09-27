# OpsAI contributor instructions

## Scope

Read `docs/OpsAI-Project-Blueprint.md` completely before changing the project.
Current scope is Milestone 1: the Go API foundation only. Do not add PostgreSQL,
Docker, devices, incidents, background workers, AI, MQTT, authentication, or later
milestones without an explicit new user request.

## Architecture

- Module: `github.com/knyongki/opsai-incident-platform`.
- `cmd/api` wires configuration, logging, HTTP startup, and shutdown.
- `internal/config` loads and validates environment configuration.
- `internal/platform/logging` constructs `log/slog` structured logging.
- `internal/transport/http` owns handlers, JSON envelopes, and HTTP server settings.
- Prefer idiomatic Go and the standard library. Ask before adding third-party dependencies.
- Keep architecture simple; introduce interfaces only where a consumer needs one.
- Future domain/service code must remain independent of transport and providers.
- Pass contexts across operations and use bounded timeouts at process boundaries.
- Return consistent JSON success/error envelopes; test HTTP behavior with `httptest`.
- Explain important Go concepts briefly for the learner.

## Workflow and validation

Inspect repository status and preserve all existing files and uncommitted work.
Give a short plan before implementation. Run these commands after Go changes:

```text
gofmt -w cmd internal
go vet ./...
go test ./...
```

Fix problems introduced by the changes. Review tracked diffs and new files for
secrets, unnecessary files, and scope violations. Report changed files, decisions,
actual validation results, assumptions/issues, and the next small task. Never
claim an unexecuted or failed check passed.

## Safety

- Never create, print, or commit real secrets, credentials, API keys, or `.env` files.
- Use only placeholder/example values in `.env.example`.
- Do not delete or overwrite unrelated files.
- Do not use destructive Git commands, including `git reset --hard`, force push,
  or branch deletion.
- Do not push to a remote repository.
- Do not create a Git commit unless the user explicitly requests it.
- Do not install system-level software or change operating-system configuration.
- Ask before adding any third-party dependency.
- Bind the server to localhost by default.
- Never log environment secrets or authorization values.
- Never silently ignore errors.
- If requirements conflict or important information is missing, stop and ask
  before making a major assumption.
