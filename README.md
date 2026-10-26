# OpsAI

OpsAI is an AI-assisted incident-analysis platform for industrial equipment,
written in Go. Implemented scope: **Milestone 1 (Go API Foundation)**,
**Milestone 2A (PostgreSQL Foundation)**, **Milestone 2B (Device Management API)**,
**Milestone 3A (Incident Creation and Durable Job Enqueueing)**, and
**Milestone 3B (Incident List and Detail APIs)**, and
**Milestone 4A (Durable Worker Foundation)**. The worker runs only a
fake analyzer against a separate synthetic test database. Real AI, analysis results,
incident update endpoints, MQTT, and authentication remain future work. See the
[blueprint](docs/OpsAI-Project-Blueprint.md) and [contributor rules](AGENTS.md).

## Start locally (PowerShell)

Run commands from the repository root. For a new computer, follow the
[Windows setup guide](docs/Local-Automation-Setup.md) to install Go, Docker Desktop,
and project-local Goose.

### First-time configuration

Create a private configuration file only if it does not exist:

```powershell
if (-not (Test-Path .env)) { Copy-Item .env.example .env }
```

Edit `.env` privately before starting. Use the keys defined in `.env.example`,
with values matching your local database. Keep DATABASE_URL and POSTGRES settings
consistent, including URL-encoding special characters in URL credentials.
The file stays Git-ignored; never commit it or overwrite existing credentials.
Existing API keys remain required; new WORKER_/JOB_ settings are optional and use
defaults when absent. Fake mode has no enabling default.

### Daily startup

Open Docker Desktop, then run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-local.ps1
```

The script loads private `.env`, starts/reuses PostgreSQL, applies pending forward
migrations, checks migration status, and runs the API. Defaults are PostgreSQL on
**5432** and the API at **http://127.0.0.1:8080**. Keep the terminal open.
Startup does not run tests or require Postman CLI.

Private file values override inherited terminal values. The scripts accept literal
KEY=VALUE lines without interpolation; the Go API reads process environment
variables. See the setup guide for syntax, terminal-only configuration, and
troubleshooting.

To send requests, follow the [Postman quickstart](docs/Postman-Quickstart.md).
For automated checks, see [local validation](#local-validation).

### Stop safely

Press **Ctrl+C** to stop the API. PostgreSQL stays running. To stop it while
preserving data:

```powershell
docker compose --env-file .env.example stop postgres
```

Run the startup command again next time. Do not delete volumes or run Goose Down
as routine cleanup. Existing database credentials are not changed by editing
Compose initialization settings.

### Health and readiness

```powershell
curl.exe -i http://127.0.0.1:8080/health
curl.exe -i http://127.0.0.1:8080/ready
```

`/health` returns HTTP 200 with `{"data":{"status":"ok"}}` while the process serves
requests. `/ready` pings PostgreSQL with a two-second deadline on every request:

- Reachable: HTTP 200, `{"data":{"status":"ready"}}`.
- Unavailable: HTTP 503,
  `{"error":{"code":"DATABASE_UNAVAILABLE","message":"The database is unavailable."}}`.

Readiness checks connectivity, not schema/migration state. Both routes support
HEAD without a response body. Application responses use `Content-Type: application/json`.
Unknown paths return HTTP 404 with
`{"error":{"code":"NOT_FOUND","message":"The requested resource was not found."}}`.
Unsupported methods on these System paths return HTTP 405 with the same error envelope
and `Allow: GET, HEAD`. Paths match exactly; `/health/` is not `/health`.
Malformed HTTP rejected before routing uses Go's protocol-level responses.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `APP_ENV` | `development` | Environment label; no behavior switch yet |
| `HTTP_ADDRESS` | `127.0.0.1:8080` | Explicit host and numeric port (1-65535) |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, or `ERROR`, case-insensitive |
| `DATABASE_URL` | Required | PostgreSQL URL with host and database |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | Required by Compose | Local initialization values |
| `POSTGRES_PORT` | `5432` | Localhost port published by Compose |

Empty optional API values use defaults. Missing or invalid required configuration
fails startup without echoing connection strings.

## Architecture and Go concepts

- `cmd/api`: entry point, dependency wiring, and graceful shutdown.
- `cmd/worker` and `internal/worker`: separate fake worker, job policy, and consumer-owned repository/analyzer contracts.
- `internal/config`: environment configuration and validation.
- `internal/platform/logging`: structured `log/slog` logging.
- `internal/device`: device model, validation, service, and repository interface.
- `internal/incident`: incident validation, creation/read services, cursor pagination, and repository contract.
- `internal/platform/database`: connection pool, startup checks, and device/incident repositories.
- `internal/platform/database/dbgen`: generated sqlc queries.
- `internal/transport/http`: handlers, envelopes, readiness interface, and server.
- `db/migrations`: reversible Goose SQL migrations.
- `db/sqlc.yaml` and `db/queries`: device/incident SQL and query generation configuration.

A pool reuses a bounded set of database connections across requests. This pool
allows 10 connections, a five-minute idle limit, and a one-hour lifetime. HTTP
timeouts remain 5 seconds for headers, 10 seconds for reads and writes, and
60 seconds for idle connections.

The HTTP package defines a small `ReadinessChecker` interface with `Ping(context.Context)`.
Go interfaces are satisfied implicitly: `pgxpool.Pool` and test doubles both provide
that method. HTTP unit tests need no database. `context.Context` carries cancellation
and deadlines; `defer pool.Close()` releases the pool after HTTP shutdown. The
`internal` directory restricts these packages' import scope.

sqlc generates pgx/v5 code from the existing migration and named queries. Install
its pinned development CLI locally (no new runtime dependency):

```powershell
$previousGoBin = $env:GOBIN
try {
    $env:GOBIN = Join-Path (Get-Location) 'bin'
    go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0
    if ($LASTEXITCODE -ne 0) { throw 'sqlc installation failed.' }
} finally {
    $env:GOBIN = $previousGoBin
}
.\bin\sqlc.exe generate -f db/sqlc.yaml
```

Generated files are committed under `internal/platform/database/dbgen`; do not edit
them manually. There are no `go:generate` directives. The original devices migration
is unchanged. The service owns validation and depends on a small repository
interface, allowing unit tests to use in-memory test doubles. The PostgreSQL
repository converts driver types and errors at its boundary.

## Device API (Milestone 2B)

Start PostgreSQL, apply migrations, and start the API using the setup above.
These PowerShell examples operate only on the device created in the first command:

```powershell
$base = 'http://127.0.0.1:8080/api/v1/devices'
$body = @{
    name = 'AMR-07'
    type = 'amr'
    location = 'Assembly line 1'
    metadata = @{ manufacturer = 'Example' }
} | ConvertTo-Json -Depth 10
$created = Invoke-RestMethod -Method Post -Uri $base -ContentType 'application/json' -Body $body
$deviceId = $created.data.id
Invoke-RestMethod -Method Get -Uri $base
Invoke-RestMethod -Method Get -Uri "$base/$deviceId"
$patch = @{ status = 'online'; location = $null } | ConvertTo-Json
Invoke-RestMethod -Method Patch -Uri "$base/$deviceId" -ContentType 'application/json' -Body $patch
Invoke-WebRequest -Method Delete -Uri "$base/$deviceId" -UseBasicParsing
```

| Method | Path | Success |
| --- | --- | --- |
| POST | `/api/v1/devices` | 201, device in `data`, and `Location` header |
| GET | `/api/v1/devices` | 200, array in `data` (empty array when none) |
| GET | `/api/v1/devices/{id}` | 200, device in `data` |
| PATCH | `/api/v1/devices/{id}` | 200, updated device in `data` |
| DELETE | `/api/v1/devices/{id}` | 204, no response body |

Creation requires nonblank `name` (up to 200 characters) and `type` (up to 50).
Optional `status` is nonblank text up to 50 characters, defaulting to `unknown`.
`location` is nullable text up to 500 characters. `metadata` is a JSON object up
to 16 KiB, defaulting to `{}`. NUL characters are rejected. Request bodies are
limited to 64 KiB. IDs must use the standard hyphenated UUID format. The service
generates random UUID v4 IDs; clients cannot set IDs or timestamps.

The schema has no enum constraints: suggested types include `amr`, `conveyor`,
`plc`, `robot`, and `other`, but other nonblank values are accepted. These length
limits are application rules; the existing database TEXT columns remain unchanged.

PATCH requires at least one field, preserves omitted fields, and accepts null only
for `location`. Supplied metadata replaces the entire object. Updates set
`updated_at` in a single SQL statement; simultaneous changes to different fields
do not overwrite omitted fields. Changes to the same field use the last update.
Lists are ordered by creation time then ID; pagination is not implemented.

Malformed JSON, multiple JSON values, unknown fields, wrong field types, invalid
IDs, and validation failures return 400 `VALIDATION_ERROR`. Missing devices return
404 `NOT_FOUND`; uniqueness violations return 409 `CONFLICT`; unexpected storage
failures return 500 `INTERNAL_ERROR`. Errors use the existing `error` envelope and
never contain database error text. The only unique constraint is the server-generated
primary key, so duplicate device names are allowed. Unsupported methods return 405.
Each device operation has a five-second context deadline.

Run the separate repository integration test against a migrated local database.
Set DATABASE_URL in the current terminal using the setup guide's
[terminal-only instructions](docs/Local-Automation-Setup.md#alternative-terminal-only-configuration).
The startup script loads private configuration only in its child process; it does
not export DATABASE_URL back to your terminal.


```powershell
$env:OPSAI_TEST_DATABASE_URL = $env:DATABASE_URL
try {
    go test -tags=integration ./internal/platform/database -run TestDeviceRepositoryIntegration -count=1
} finally {
    Remove-Item Env:OPSAI_TEST_DATABASE_URL
}
```

The test requires the explicit `integration` build tag and environment variable.
It checks CRUD, partial updates, missing records, and uniqueness mapping inside a
transaction which is rolled back. Normal `go test ./...` needs no database.

## Incident creation (Milestone 3A)

`POST /api/v1/incidents` stores an incident and its initial analysis job in one
PostgreSQL transaction. A transaction makes both inserts succeed together or
roll back together; HTTP 202 is returned only after commit. SQL calls use the
same transaction-bound sqlc instance. IDs are server-generated UUID v4 values.
The service remains independent of HTTP and PostgreSQL.

Migration `00002_create_incidents_and_analysis_jobs.sql` adds both tables without
changing the devices migration. It adds severity/status checks, a nonnegative
attempt count, device and incident foreign keys, one-job-per-incident uniqueness,
a device-reference index, and an index of pending jobs by availability time.
Deleting a device referenced by an incident now returns 409 `CONFLICT`.

| Field | Contract |
| --- | --- |
| `device_id` | Required UUID of an existing device |
| `title` | Required, nonblank, at most 200 Unicode characters |
| `description` | Required, nonblank, at most 10,000 characters |
| `severity` | Required: `low`, `medium`, `high`, or `critical` |
| `alarm_code` | Optional/null; at most 100 characters |
| `raw_logs` | Optional (defaults to `[]`); at most 100 strings of 1,000 characters each; null array/entries rejected |
| `occurred_at` | Optional/null; RFC3339 timestamp with offset; normalized to UTC; future dates allowed |

NUL and invalid UTF-8 text, unknown fields, malformed/multiple JSON values, and
bodies larger than 256 KiB are rejected with 400 `VALIDATION_ERROR`. A missing
device returns 404 `NOT_FOUND`; unexpected failures return a sanitized 500
`INTERNAL_ERROR`. The collection route also supports GET (see below); other methods
return 405 with `Allow: GET, POST`.
Clients cannot supply IDs, status, attempt counts, or server timestamps.

The response is `{"data":{"id":"INCIDENT_UUID","status":"open","analysis_status":"queued"}}`.
`queued` means a job with stored status `pending` exists. Normal API startup does
not start a worker. The explicit 4A fake worker uses a separate test database.
Repeating POST creates a new incident and job;
idempotency is not implemented. Worker retries update the same job row;
separate reanalysis history needs a later schema decision.

From a terminal with DATABASE_URL configured using the
[terminal-only instructions](docs/Local-Automation-Setup.md#alternative-terminal-only-configuration),
matching any private `.env`, apply forward migrations and run
System checks, then run device, incident creation, and incident read integration tests:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
if ($LASTEXITCODE -ne 0) { throw 'Migration/System validation failed.' }
$previousTestURL = $env:OPSAI_TEST_DATABASE_URL
try {
    $env:OPSAI_TEST_DATABASE_URL = $env:DATABASE_URL
    go test -tags=integration ./internal/platform/database -run 'Test(DeviceRepository|IncidentRepository|IncidentReads)Integration' -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Repository integration tests failed.' }
} finally {
    $env:OPSAI_TEST_DATABASE_URL = $previousTestURL
    Remove-Variable previousTestURL
}
```

These integration tests need all current migrations applied. The incident test commits
synthetic records and checks visibility from another connection, deliberately
fails a job insert to prove rollback, rejects duplicate jobs, and verifies device
deletion protection. Cleanup deletes only rows created by that test. Tests skip
without the test URL; a skipped test is not evidence of transaction correctness.
The read integration test uses rolled-back fixtures for timestamp ties, pagination,
all filters, status mapping, complete detail, and missing incidents/jobs.

For a manual API check, start the API with `scripts/start-local.ps1` as shown above.
In another terminal, use synthetic data (these records persist):

```powershell
$base = 'http://127.0.0.1:8080'
$deviceBody = @{ name = 'Demo incident device'; type = 'amr' } | ConvertTo-Json
$demoDevice = Invoke-RestMethod -Method Post -Uri "$base/api/v1/devices" -ContentType 'application/json' -Body $deviceBody
$incidentBody = @{
    device_id = $demoDevice.data.id
    title = 'Demo docking failure'
    description = 'Synthetic incident for local verification.'
    severity = 'high'
    raw_logs = @('Synthetic docking log')
    occurred_at = '2026-09-14T09:30:00Z'
} | ConvertTo-Json
$response = Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$base/api/v1/incidents" -ContentType 'application/json' -Body $incidentBody
$response.StatusCode # 202
$response.Content
```

The local Postman collection also has an **Incidents - Manual** folder. Run it
explicitly; it creates its own device before the incident. Normal automation still
runs only System:

```powershell
postman collection run 'api/postman/OpsAI API' -i 'Incidents - Manual' --env-var 'base_url=http://127.0.0.1:8080' --no-report-events
```

This creates a persistent device, incident, and job on each run. The folder uses
incident_device_id independently of the device CRUD folder's device_id and cleanup.
No incident delete endpoint exists, and device deletion is restricted afterward.
Use the integration test for repeatable verification with automatic fixture cleanup.

## Incident reads (Milestone 3B)

Both read endpoints return HTTP 200 in the existing `data` envelope and have a
five-second operation timeout. They do not modify incidents or jobs.

| Method and path | Result |
| --- | --- |
| `GET /api/v1/incidents` | `{ "data": { "items": [], "page": { "next_cursor": null, "has_more": false } } }` when empty |
| `GET /api/v1/incidents/{id}` | Complete incident in `data`; invalid UUID returns 400, missing incident returns 404 |

### List parameters and pagination

| Parameter | Contract |
| --- | --- |
| `limit` | Decimal integer, default 20, range 1-100 |
| `cursor` | Opaque `next_cursor` from the preceding page; omit for the first page |
| `device_id` | Exact hyphenated UUID; UUID letter case is normalized |
| `status` | Exact `open`, `analyzing`, `analyzed`, `analysis_failed`, or `resolved` |
| `severity` | Exact `low`, `medium`, `high`, or `critical` |
| `alarm_code` | Exact case-sensitive text, maximum 100 Unicode characters; NUL/invalid UTF-8 rejected |

Filters combine with AND. Empty supplied values are invalid except `alarm_code=`:
that matches a stored empty string, not NULL. Omit alarm_code to include all values.
Malformed cursors, invalid filters/limits, duplicate parameters, unknown parameters,
and malformed URL query encoding return 400 `VALIDATION_ERROR`. Detail accepts no
query parameters. Unsupported methods return 405 (`Allow: GET` on detail).

Rows are ordered by `created_at DESC, id DESC`. Each query fetches `limit + 1`
summary rows; the extra row determines `has_more`. If true, `next_cursor` represents
the last returned row. Otherwise it is null. Subsequent queries use the strict
tuple boundary `(created_at, id) < (cursor timestamp, cursor UUID)`, with no offset.
This tie-breaker prevents skipped or duplicated rows with equal timestamps.

The versioned cursor is canonical JSON encoded with unpadded base64url. It holds
the timestamp, UUID, and SHA-256 of normalized filters. Clients must treat it as
opaque and resend the same filters; changed or omitted filters return 400.
The page size may change. The cursor is not signed or an authorization token.
It does not provide a database snapshot across requests: newly inserted rows ahead
of the boundary require restarting from the first page, and changing filter fields
can change membership between pages.

### Response fields

List items contain only `id`, `device_id`, `title`, `alarm_code`, `severity`,
`status`, `analysis_status`, `occurred_at`, `created_at`, and `updated_at`.
They omit description and raw logs. `items` is always an array.

Detail adds `description`, `raw_logs`, and `resolved_at`. Optional alarm_code,
occurred_at, and resolved_at serialize as JSON null when absent; raw_logs is an
array, including `[]` when empty. Timestamps are returned in UTC.

The existing job row supplies analysis_status: `pending` maps to `queued`;
`running`, `completed`, and `failed` retain their names. No job IDs, attempt counts,
lock timestamps, or internal errors are exposed. A missing/invalid job is treated
as a storage invariant failure (sanitized 500 `INTERNAL_ERROR`), rather than hiding
the incident. A nonexistent incident returns 404 `NOT_FOUND`.

The sqlc queries `ListIncidents` and `GetIncident` read the existing tables with a
left join to analysis_jobs. No schema migration or dependency was added. Existing
indexes are sufficient for correctness; large datasets may need a measured index
improvement later. Performance/load testing is not part of this milestone.

### Try list and detail

After creating an incident using the example above:

```powershell
$incidentId = ($response.Content | ConvertFrom-Json).data.id
Invoke-RestMethod "$base/api/v1/incidents/$incidentId"
$filters = "device_id=$($demoDevice.data.id)&status=open&severity=high"
$firstPage = Invoke-RestMethod "$base/api/v1/incidents?limit=1&$filters"
$firstPage.data.items
if ($firstPage.data.page.has_more) {
    $cursor = [Uri]::EscapeDataString($firstPage.data.page.next_cursor)
    Invoke-RestMethod "$base/api/v1/incidents?limit=1&$filters&cursor=$cursor"
}
```

Alternatively run the local **Incidents - Manual** Postman folder: its setup saves
incident_id, then the read requests check list, combined filters, complete detail,
invalid UUID, and a missing incident. The 3B requests were synchronized to the cloud
collection on explicit request and fetched back for verification. Automatic
validation continues to run only **System**; later edits are not automatically synced.

To run the device and incident repository integration tests with private .env in an isolated process:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command {
    $ErrorActionPreference = 'Stop'
    . ./scripts/local-environment.ps1
    $settings = Read-LocalEnvironment -Root (Get-Location).Path
    $env:OPSAI_TEST_DATABASE_URL = $settings.DATABASE_URL
    go test -tags=integration ./internal/platform/database `
        -run 'Test(DeviceRepository|IncidentRepository|IncidentReads)Integration' -count=1 -v
    exit $LASTEXITCODE
}
```

PostgreSQL must be running with all current migrations applied. Require all three
tests to report PASS, not SKIP. Normal `go test ./...` remains database-independent.

## Fake worker (Milestone 4A)

Use the [worker setup instructions](docs/Local-Automation-Setup.md#isolated-fake-worker-milestone-4a)
to create and migrate a separate localhost database ending in `_worker_test`.
Set private `WORKER_DATABASE_URL` to that database and explicitly set
`WORKER_ANALYZER=fake`. The worker never falls back to DATABASE_URL.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-worker.ps1 -MigrateOnly
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-worker.ps1
```

The first command validates worker configuration and applies forward migrations
to the separate worker database without starting work. The second runs one job
at a time. Ctrl+C cancels active work and stops claiming. Start a second worker
terminal only against the same synthetic database and with matching policy.
Regular `start-local.ps1` remains API-only.

Claims use short transactions and SKIP LOCKED. Migration 00003 adds a fresh UUID
claim token, lease expiry, ownership constraint, and expired-running-job index.
It rejects incompatible legacy ownership rather than resetting records.
Analysis runs outside the transaction. Final writes require the current token
and unexpired lease; expired or stale workers cannot overwrite newer owners.
Recovery processes up to 100 expired/exhausted jobs per pass. All transition times
come from PostgreSQL. Shutdown leaves unfinished claims for lease recovery.

Delivery permits repeated execution after a crash; a claim consumes an attempt
even if the process dies before analysis. Retries have a fixed cap, so successful
processing is not guaranteed. No heartbeat or exactly-once guarantee exists.
Storage failures use bounded backoff; uncertain completion is recovered rather
than immediately re-executed.

| Setting | Default | Meaning |
| --- | --- | --- |
| WORKER_ANALYZER | Empty; must explicitly be fake | Enables only the fake implementation |
| WORKER_DATABASE_URL | Required for worker | Separate localhost database ending in _worker_test |
| WORKER_FAKE_OUTCOME | success | success, transient, permanent, or transient-once |
| WORKER_FAKE_DELAY | 100ms | Simulated work; cancellation-aware |
| JOB_POLL_INTERVAL | 1s | Idle polling delay |
| JOB_ATTEMPT_TIMEOUT | 30s | Analyzer deadline |
| JOB_LEASE | 60s | Fixed ownership duration |
| JOB_DATABASE_TIMEOUT | 5s | Bounded storage operation |
| JOB_RETRY_BASE / JOB_RETRY_CAP | 2s / 60s | Capped exponential retry delay |
| JOB_MAX_ATTEMPTS | 3 | Claim limit, 1-100 |
| WORKER_SHUTDOWN_TIMEOUT | 10s | Maximum wait for cancellation |

Policy durations must be 1ms-24h; fake delay may also be zero. Lease must exceed
attempt timeout plus two database timeouts, and retry cap must be at least base.
URLs allow only sslmode, connect_timeout, and application_name query parameters
to prevent connection overrides bypassing the isolated-database check.

Fake success moves only the job to completed. Incidents remain open; no analysis
result is generated. Transient failures and attempt timeouts retry until the cap;
permanent/unclassified failures stop immediately. The existing GET APIs show
queued/running/completed/failed without exposing tokens, attempts, or errors.
In this demo, completed means fake processing completed, not real findings.
Safe error codes are stored internally; incident bodies and raw errors are not logged.

Stop all workers before repository tests. Use an otherwise empty queue in a
dedicated migrated test database; tests refuse to consume existing pending work:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command {
    $ErrorActionPreference = 'Stop'
    . ./scripts/local-environment.ps1
    $settings = Read-LocalEnvironment -Root (Get-Location).Path
    $env:OPSAI_TEST_DATABASE_URL = $settings.WORKER_DATABASE_URL
    $env:OPSAI_WORKER_TEST_DATABASE_URL = $settings.WORKER_DATABASE_URL
    go test -tags=integration ./internal/platform/database -count=1 -v
    exit $LASTEXITCODE
}
```

Require PASS rather than SKIP for device, incident creation/reads, job repository,
and lease migration tests. Job tests use committed synthetic fixtures and separate
connections; cleanup touches only their own records. Migration compatibility tests
use isolated schemas rolled back afterward. Normal `go test ./...` needs no database.

## Local validation

With the [setup prerequisites](docs/Local-Automation-Setup.md) installed and private
`.env` configured, run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
```

The runner checks Go formatting, runs vet and unit tests, validates Compose,
starts/reuses PostgreSQL with `--no-recreate`, applies forward migrations, checks
migration status, and runs the Postman **System** folder against a temporary API
on **127.0.0.1:18080**. It stops its own API afterward and leaves PostgreSQL running.
The database port comes from POSTGRES_PORT (normally 5432), unless overridden with
`-DatabasePort`; DATABASE_URL must match. Artifacts and logs remain in ignored
`bin/`. The runner installs no tools and fails on command or assertion errors.

To check without starting services or reading private configuration:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1 -ChecksOnly
```

The [versioned collection](api/postman/OpsAI%20API) includes
System, device, and manual incident requests. Only System runs automatically:
health and readiness status, JSON content type, and exact success envelopes.
Readiness proves connectivity; Goose checks migrations separately. Repository
integration tests require the explicit commands above.

The runner uses the local collection and disables run-event reporting. It does
not publish edits, synchronize with Postman, or configure CI. Preserve existing
collection/request IDs; cloud synchronization requires an explicit request.

After Go changes, also apply formatting and update module metadata:

```powershell
gofmt -w cmd internal
go mod tidy
go vet ./...
go test ./...
docker compose --env-file .env.example config --quiet
```

Unit tests require no container. They cover configuration, logging, HTTP contracts,
readiness success/failure and cancellation, and graceful HTTP shutdown. Database
health, migrations, and live readiness must be checked separately using the setup
commands; passing unit tests alone does not prove PostgreSQL connectivity.

For milestone status, verification evidence, and remaining review work, see
[the task list](docs/Tasks.md).

References: [pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool),
[Goose](https://pressly.github.io/goose/documentation/cli-commands/), and
[sqlc configuration](https://docs.sqlc.dev/en/latest/reference/config.html).
