# OpsAI contributor instructions

## Scope

Read `docs/OpsAI-Project-Blueprint.md` completely before changing the project.
Current scope includes Milestone 1 (Go API foundation), Milestone 2A (PostgreSQL
foundation), Milestone 2B (device management), and local validation automation.
Milestone 2A includes PostgreSQL Compose setup, pgx/v5 pooling, database readiness,
Goose migrations for the devices table, and sqlc configuration. Milestone 2B
includes device create, list, get, update, and delete endpoints, domain validation,
repository queries, generated sqlc code, and unit/handler/repository integration tests.
Local automation includes the versioned Postman collection, the PowerShell runner,
and setup documentation. Automated Postman execution currently covers only the
System folder (health and readiness); preserve the saved device folders.
Milestone 3/3A is not yet authorized. Do not add incidents, workers, AI, MQTT,
authentication, cloud synchronization, CI workflows, or later features without
an explicit new user request. The blueprint is a roadmap, not blanket authorization.

## Architecture

- Module: `github.com/knyongki/opsai-incident-platform`.
- `cmd/api` wires configuration, logging, HTTP startup, and shutdown.
- `internal/config` loads and validates environment configuration.
- `internal/platform/logging` constructs `log/slog` structured logging.
- `internal/device` owns device models, validation, service logic, and the repository interface.
- `internal/platform/database` owns PostgreSQL pooling, startup checks, and the device repository.
- `db/migrations` holds Goose migrations; `db/queries` holds SQL queries and
  `db/sqlc.yaml` configures generation into `internal/platform/database/dbgen`.
  Regenerate sqlc output rather than editing generated files manually.
- `internal/transport/http` owns handlers, JSON envelopes, and HTTP server settings.
- `api/postman/opsai.postman_collection.json` is the versioned local API collection.
- `scripts/check-local.ps1` runs local validation; `docs/Local-Automation-Setup.md`
  documents setup on another Windows device.
- Prefer idiomatic Go and the standard library. Ask before adding third-party dependencies.
  The user has authorized pgx/v5, Goose, and sqlc for the PostgreSQL foundation.
- Keep architecture simple; introduce interfaces only where a consumer needs one.
- Domain/service code must remain independent of transport and providers.
- Pass contexts across operations and use bounded timeouts at process boundaries.
- Return consistent JSON success/error envelopes; test HTTP behavior with `httptest`.
- Explain important Go concepts briefly for the learner.

## Workflow and validation

Inspect repository status and preserve all existing files and uncommitted work.
Give a short plan before implementation. Run these commands after Go changes:

```text
gofmt -w cmd internal
go mod tidy
go vet ./...
go test ./...
docker compose config --quiet
```

Fix problems introduced by the changes. Review tracked diffs and new files for
secrets, unnecessary files, and scope violations. Report changed files, decisions,
actual validation results, assumptions/issues, and the next small task. Never
claim an unexecuted or failed check passed.
Use the documented example environment for configuration-only Compose checks.
For live checks, preserve configured local credentials; never replace an existing
database password with the example value. Keep unit tests independent of PostgreSQL.
For runtime, database, or automation changes, verify container health, migration
status, and live API readiness when the required tools and credentials are available.
Report unavailable tools and unrun checks. Documentation-only changes need a diff
and consistency review, not a database startup.

Use the local runner from a terminal with DATABASE_URL configured securely:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
```

The runner defaults to PostgreSQL on localhost:5432 and a temporary API on
localhost:18080. It preserves existing container configuration with --no-recreate,
applies forward migrations, runs the Postman System folder, stops its own API,
and leaves PostgreSQL running. It does not load .env or publish collection changes.
Follow docs/Local-Automation-Setup.md for prerequisites and hidden credential input.
Never ask the user to paste credentials into chat.

Use -ChecksOnly for formatting, vet, unit tests, and Compose configuration checks
without starting services. The runner checks formatting but does not run gofmt -w
or go mod tidy; it does not replace those required steps after Go changes.
Device repository integration tests are separate: use the explicit integration
build tag and OPSAI_TEST_DATABASE_URL as documented in README.md. A passing System
folder does not establish device API or repository integration coverage.
Preserve collection/request IDs and review related local collection changes with
API changes. Cloud publication and synchronization require a separate user request.

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
- Never print DATABASE_URL or raw database driver errors; they may contain secrets.
- Keep `.env` ignored and `.env.example` tracked.
- Do not delete Docker volumes or run `docker compose down -v`.
- Do not execute destructive database commands. A Goose Down migration is provided
  for reversibility but must not be run without explicit authorization.
- Never silently ignore errors.
- If requirements conflict or important information is missing, stop and ask
  before making a major assumption.

## Public repository hygiene

- Treat every tracked file as publicly readable, including documentation, scripts,
  API collections, saved responses, fixtures, screenshots, and generated reports.
- Use repository-relative paths or clearly generic example paths in new documentation.
  Do not add personal workstation paths, private workspace names, account/tenant
  identifiers, internal hostnames, or private resource links unless they are needed
  for the project and explicitly approved for publication. Existing public project
  identifiers such as the Go module path are intentional and should be preserved.
- Prefer links to versioned local API files over links to a contributor's personal
  Postman workspace. Collection/request IDs are not credentials; preserve existing
  IDs needed for stable tooling, but do not copy them into prose unnecessarily.
- Before adding API exports or examples, inspect authorization fields, headers,
  cookies, URL query parameters, variables, scripts, and saved response bodies for
  secrets or real user/customer data. Use synthetic data and localhost/example URLs.
- Never embed access tokens, signed URLs, private keys, real passwords, or connection
  strings with real credentials in tracked files. Document environment variable
  names and hidden input prompts instead. Keep local logs and reports ignored.
- Before a requested commit, review the exact staged diff and new files for sensitive
  content and verify that local credentials and build artifacts remain excluded.
  An ignore rule does not protect a file already tracked by Git.
- If sensitive content is found, report its location and category without repeating
  the value. Removing it from the working tree does not remove it from Git history.
  For an exposed real credential, recommend revocation/rotation; do not rewrite
  history, force-push, or change external credentials without explicit authorization.
- State the scope of any safety review. Do not describe a documentation review or
  pattern search as a complete repository/history secrets audit.
