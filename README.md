# OpsAI

**AI-assisted incident analysis for industrial equipment.**

When an AMR, conveyor, or PLC-connected machine raises an alarm, engineers
usually piece together logs, symptoms, and past fixes by hand. OpsAI gives that
work a home: you register equipment, report an incident with its alarm and logs,
and a background worker analyzes it and attaches a structured, validated
troubleshooting result to the incident.

OpsAI is decision support for troubleshooting and incident documentation. It does
not replace qualified engineering judgment.

> **Current status:** the analysis pipeline is complete end to end, but it runs
> a deterministic **fake analyzer** for development and testing. Results are
> labeled `source: "fake"`, state that no diagnosis was performed, and have
> confidence 0. Real AI providers are on the [roadmap](#roadmap).

## What you can do today

- **Manage devices:** register, list, update, and delete equipment records.
- **Report incidents:** submit an alarm code, description, severity, and raw logs.
  The incident and its analysis job are saved together in one transaction.
- **Browse incidents:** filter by device, status, severity, or alarm code, with
  stable cursor pagination.
- **Get structured analysis:** a separate worker processes each job and stores a
  strictly validated result, shown on the incident as `analysis`.

## How it works

```text
            HTTP (JSON)                        claims jobs
 Client ───────────────▶  API  (cmd/api)        Worker (cmd/worker)
                           │                      │   │
            incident + job │ one transaction      │   │ analyzer (fake today)
                           ▼                      ▼   ▼
                      ┌──────────────────── PostgreSQL ───────────────────┐
                      │ devices · incidents · analysis_jobs ·             │
                      │ incident_analyses (one validated result/incident) │
                      └───────────────────────────────────────────────────┘
```

1. `POST /api/v1/incidents` stores the incident and a `pending` job atomically and
   returns `202` with `analysis_status: "queued"`.
2. The worker claims the job with a lease and claim token, loads only that
   incident, and runs the analyzer outside any database transaction.
3. The output must pass a strict, versioned contract. A valid result is stored in
   the same transaction that completes the job, so there is never a completed job
   with a half-written result. Invalid output fails the job and stores nothing.
4. `GET /api/v1/incidents/{id}` returns the incident, its job status, and the
   stored analysis from one consistent read.

Retries are bounded with exponential backoff, expired leases are recovered, and a
stale worker can never overwrite a newer one. Details:
[Worker and analysis](docs/Worker-and-Analysis.md).

### Technology choices

| Choice | Why |
| --- | --- |
| Go standard library (`net/http`, `log/slog`, `encoding/json`) | Small dependency surface, explicit behavior |
| PostgreSQL 17 with pgx/v5 | Transactions, `SKIP LOCKED` job claiming, JSONB results |
| A PostgreSQL job table instead of a message broker | One system to run; the job and incident commit together |
| Goose migrations, sqlc-generated queries | Reviewable SQL with type-safe Go code |
| Separate worker process and test database | Analysis never blocks HTTP requests; fake runs cannot touch the normal queue |

## Quick start (Windows PowerShell)

Requirements: Go 1.26.5 or newer, Docker Desktop, and project-local Goose. The
[setup guide](docs/Local-Automation-Setup.md) installs everything step by step.

```powershell
# 1. Create private configuration once, then edit it with your local values.
if (-not (Test-Path .env)) { Copy-Item .env.example .env }

# 2. Start PostgreSQL, apply migrations, and run the API on http://127.0.0.1:8080.
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-local.ps1

# 3. In another terminal, check it is up.
curl.exe -i http://127.0.0.1:8080/ready
```

`.env` is Git-ignored; never commit it. Then create a device and an incident with
the [Postman quickstart](docs/Postman-Quickstart.md) or the PowerShell examples in
the [API reference](docs/API-Reference.md). Press Ctrl+C to stop the API;
PostgreSQL keeps running and keeps its data.

To see analysis results, run the fake worker against its separate test database:
[isolated worker setup](docs/Local-Automation-Setup.md#isolated-fake-worker).

## API at a glance

| Method and path | Purpose |
| --- | --- |
| `GET /health`, `GET /ready` | Process liveness; database readiness |
| `POST /api/v1/devices` | Register a device |
| `GET /api/v1/devices`, `GET /api/v1/devices/{id}` | List or read devices |
| `PATCH /api/v1/devices/{id}`, `DELETE /api/v1/devices/{id}` | Update or delete a device |
| `POST /api/v1/incidents` | Report an incident; queues its analysis |
| `GET /api/v1/incidents` | List incidents with filters and cursor pagination |
| `GET /api/v1/incidents/{id}` | Full incident, job status, and `analysis` |

Example incident detail after analysis (fields shortened):

```json
{
  "data": {
    "id": "00000000-0000-4000-8000-000000000001",
    "title": "Demo docking failure",
    "severity": "high",
    "status": "open",
    "analysis_status": "completed",
    "analysis": {
      "schema_version": 1,
      "source": "fake",
      "result": {
        "summary": "...",
        "confidence": 0,
        "evidence": [{ "source_id": "00000000-0000-4000-8000-000000000001", "claim": "..." }]
      }
    }
  }
}
```

Every field, limit, and error code is in the [API reference](docs/API-Reference.md).

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `APP_ENV` | `development` | Environment label; no behavior switch yet |
| `HTTP_ADDRESS` | `127.0.0.1:8080` | Explicit host and numeric port; localhost by default |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, or `ERROR`, case-insensitive |
| `DATABASE_URL` | Required | PostgreSQL URL with host and database |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | Required by Compose | Local initialization values |
| `POSTGRES_PORT` | `5432` | Localhost port published by Compose |

Missing or invalid required settings fail startup without echoing connection
strings. Worker settings (`WORKER_*`, `JOB_*`) are optional for the API and listed
in [Worker and analysis](docs/Worker-and-Analysis.md#configuration).

## Testing

```powershell
# Formatting, vet, unit tests, and Compose checks; no services needed.
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1 -ChecksOnly

# Full local validation: also PostgreSQL, migrations, and the Postman System folder.
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
```

Unit tests need no database. PostgreSQL integration tests (transactions, job
leases, stored analysis) run with an explicit build tag; see
[repository integration tests](docs/Local-Automation-Setup.md#repository-integration-tests).
The versioned Postman collection in [`api/postman/OpsAI API`](api/postman/OpsAI%20API)
covers system, device, incident, and worker-result requests; only **System** runs
automatically.

## Safety and limitations

- Fake analysis is test output only. It performs no diagnosis and must not be
  used for operational decisions.
- Validation checks structure and that evidence references the incident itself;
  it does not prove a diagnosis is correct.
- No authentication yet: the API binds to localhost by default and is meant for
  local development.
- Database errors and connection strings are never returned or logged.
- Execution is at least once: a crash can repeat analysis, but at most one result
  is stored per incident.

## Project layout

| Path | Contents |
| --- | --- |
| `cmd/api`, `cmd/worker` | Entry points: configuration, wiring, graceful shutdown |
| `internal/device`, `internal/incident` | Domain models, validation, and services |
| `internal/analysis` | Versioned analysis contract and strict decoder |
| `internal/worker` | Job processing, retry policy, fake analyzer |
| `internal/platform/database` | Connection pool and PostgreSQL repositories (`dbgen` is generated by sqlc) |
| `internal/transport/http` | Handlers, JSON envelopes, and server settings |
| `internal/config`, `internal/platform/logging` | Environment configuration and `log/slog` setup |
| `db/migrations`, `db/queries` | Goose migrations and sqlc queries |
| `scripts/` | Local startup, worker, and validation scripts |

Domain and service code do not depend on HTTP or PostgreSQL. Each package defines
small interfaces only where it consumes them; Go satisfies interfaces implicitly,
so `pgxpool.Pool` and test doubles both fit. `context.Context` carries deadlines
and cancellation across every operation. The pool allows 10 connections (five-minute
idle limit, one-hour lifetime); HTTP timeouts are 5 seconds for headers, 10 seconds
for reads and writes, and 60 seconds for idle connections.

## Roadmap

Planned work from the [project blueprint](docs/OpsAI-Project-Blueprint.md):

- Real AI provider with a versioned prompt, structured output, and tool calling.
- Incident lifecycle transitions (analyzing, analyzed, analysis failed).
- Retrieval of similar resolved incidents, engineer review, and resolution.
- Authentication and roles, MQTT telemetry, OpenAPI docs, and CI.

Milestone status is tracked in [docs/Tasks.md](docs/Tasks.md). Contributor rules
are in [AGENTS.md](AGENTS.md).

## License

[MIT](LICENSE)
