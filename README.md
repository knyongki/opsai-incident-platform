# OpsAI

OpsAI is an AI-assisted incident-analysis platform for industrial equipment,
written in Go. Implemented scope: **Milestone 1 (Go API Foundation)**,
**Milestone 2A (PostgreSQL Foundation)** and **Milestone 2B (Device Management API)**. Incidents, workers,
AI, MQTT, and authentication remain future work. See the
[blueprint](docs/OpsAI-Project-Blueprint.md) and [contributor rules](AGENTS.md).

## Local setup (PowerShell)

Setting up another computer? Follow the [new-device automation guide](docs/Local-Automation-Setup.md)
for prerequisites, hidden password entry, one-command checks, and troubleshooting.

Prerequisites: Go 1.26.5 or newer and Docker Desktop running Linux containers
with Docker Compose. Run commands from the repository root. No system-wide tool
installation or PATH changes are needed. All values below are public local
examples, not production credentials.

### 1. Configure the current terminal

```powershell
cd D:\Yongki\opsai-incident-platform
$env:APP_ENV = 'development'
$env:HTTP_ADDRESS = '127.0.0.1:8080'
$env:LOG_LEVEL = 'INFO'
$env:POSTGRES_DB = 'opsai'
$env:POSTGRES_USER = 'opsai'
$env:POSTGRES_PASSWORD = 'local-example-only'
$env:POSTGRES_PORT = '5432'
$env:DATABASE_URL = 'postgres://opsai:local-example-only@127.0.0.1:5432/opsai?sslmode=disable'
```

Variables apply only to this terminal and its children. Repeat this setup in a
new terminal when running Compose, migrations, or the API. The API reads process
variables, not `.env` files. `.env.example` documents safe values; keep `.env`
ignored and never commit it. Do not print real connection strings or credentials.
`sslmode=disable` is only for this localhost setup.

### 2. Start PostgreSQL and check health

```powershell
docker version
docker compose version
docker compose config --quiet
docker compose up -d --wait --wait-timeout 90 postgres
docker compose ps
docker compose exec -T postgres pg_isready -U $env:POSTGRES_USER -d $env:POSTGRES_DB
```

`config --quiet` validates without printing interpolated passwords. Wait for
`healthy` in `compose ps`. PostgreSQL 17 binds to `127.0.0.1:5432` and stores data
in the named `postgres_data` volume (prefixed by the Compose project name).
`pg_isready` checks server health; the API additionally performs an authenticated
`Ping` using `DATABASE_URL`.

If port 5432 is occupied, set a free `POSTGRES_PORT` and update `DATABASE_URL` to
match. For example, before starting PostgreSQL:

```powershell
$env:POSTGRES_PORT = '15432'
$env:DATABASE_URL = 'postgres://opsai:local-example-only@127.0.0.1:15432/opsai?sslmode=disable'
```

Initialization variables apply only to an empty volume; changing them
later does not rename users or change existing passwords. Do not delete the
volume to resolve a configuration mismatch.

### 3. Install local Goose and run migrations

Goose is a development CLI, not an API runtime dependency. This pinned command
installs to the ignored project `bin` directory and excludes unused drivers:

```powershell
$previousGoBin = $env:GOBIN
try {
    $env:GOBIN = Join-Path (Get-Location) 'bin'
    go install -tags 'no_clickhouse,no_libsql,no_mssql,no_mysql,no_sqlite3,no_vertica,no_ydb' github.com/pressly/goose/v3/cmd/goose@v3.28.0
    if ($LASTEXITCODE -ne 0) { throw 'Goose installation failed.' }
} finally {
    $env:GOBIN = $previousGoBin
}
.\bin\goose.exe -version
$env:GOOSE_DRIVER = 'postgres'
$env:GOOSE_DBSTRING = $env:DATABASE_URL
.\bin\goose.exe -dir .\db\migrations status
.\bin\goose.exe -dir .\db\migrations up
if ($LASTEXITCODE -ne 0) { throw 'Migration failed; stop before starting the API.' }
.\bin\goose.exe -dir .\db\migrations status
```

The first migration creates only `devices`: UUID primary key, required name and
type, nullable location, default `unknown` status, JSONB metadata, and timestamps.
Future application code supplies IDs; no UUID extension or automatic `updated_at`
trigger is added. Goose records applied versions in its own table. The Down
section reverses table creation but destroys its data; do not run it during normal
setup or validation. The API does not apply migrations automatically.

### 4. Start the API

```powershell
go run ./cmd/api
```

Startup must reach PostgreSQL within five seconds or exits with a sanitized error.
The API listens on `127.0.0.1:8080`, logging JSON to standard error. In another terminal:

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
Unsupported methods on known paths return HTTP 405 with the same error envelope
and `Allow: GET, HEAD`. Paths match exactly; `/health/` is not `/health`.
Malformed HTTP rejected before routing uses Go's protocol-level responses.

### 5. Stop safely

Press Ctrl+C in the API terminal. SIGTERM also triggers shutdown on supported
systems. HTTP requests have up to 10 seconds to drain before the database pool
closes. In a terminal configured as in step 1:

```powershell
docker compose stop postgres
```

This retains the container and named volume. To restart:

```powershell
docker compose up -d --wait --wait-timeout 90 postgres
```

Never delete volumes as a normal stop/reset step. To test an outage, stop PostgreSQL
while the API runs: `/health` stays 200 and `/ready` becomes 503. After restarting
PostgreSQL, `/ready` should return 200 again.

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
- `internal/config`: environment configuration and validation.
- `internal/platform/logging`: structured `log/slog` logging.
- `internal/device`: device model, validation, service, and repository interface.
- `internal/platform/database`: connection pool, startup checks, and device repository.
- `internal/platform/database/dbgen`: generated sqlc queries.
- `internal/transport/http`: handlers, envelopes, readiness interface, and server.
- `db/migrations`: reversible Goose SQL migrations.
- `db/sqlc.yaml` and `db/queries`: device SQL and query generation configuration.

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

Run the separate repository integration test against a migrated local database:

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

## Validation

### One-command local validation (PowerShell)

With Go, Docker Desktop, Postman CLI, and the project-local Goose installed,
run this in the terminal where you configured `DATABASE_URL` for your existing
local PostgreSQL on port **5432** (see local setup above). Use its actual local
credentials, not the example password if your database was initialized differently:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
```

The runner checks formatting without rewriting files, runs `go vet` and unit
tests, validates Compose, starts PostgreSQL, applies forward migrations, checks
migration status, builds a temporary API, and runs the **System** folder from
[the versioned Postman collection](api/postman/opsai.postman_collection.json).
It fails on command errors or failed assertions. No tools are installed by the
runner. Postman CLI 1.75.0 was used to validate the command options.
The execution-policy override applies only to this PowerShell process; it does
not change your machine's execution policy.

It preserves configured database credentials from the process environment and
uses `.env.example` defaults for missing settings. Full runs require an explicitly
configured `DATABASE_URL`; the runner never substitutes the example connection
string for your database. It does not read `.env` or print the connection string.
Temporary environment changes are restored afterward. The API binds
to `127.0.0.1:18080` to avoid the usual development port; an occupied port fails
the run. PostgreSQL uses the existing Compose project and port 5432. The runner
starts/reuses the container with `--no-recreate` to preserve its configuration.
The connection string must target localhost and match `-DatabasePort` (5432 by
default). Forward migrations apply to this database; no rollback or volume
deletion runs. For a fresh container, configure the POSTGRES variables to match
your connection string before running.

The temporary API is stopped in `finally`, including after a test failure.
On Windows this terminates the owned test process; it does not test graceful
shutdown. PostgreSQL stays running for reuse. Use the existing safe stop
instructions when you want to stop it.
Build artifacts and API logs
remain in ignored `bin/`. Use `-ChecksOnly` for formatting, vet, unit tests, and
Compose configuration checks without starting services or running Postman.

The collection came from [OpsAI API in Postman](https://go.postman.co/collection/18505628-b1822baf-458d-4614-b5c3-a56844ba4a88)
in My Workspace (`0ba5a650-d0b9-4074-80d8-1a655028ee1f`). Its collection/request
IDs and existing device folders are preserved. Only **System** runs automatically:
GET `/health` and GET `/ready`, checking HTTP status, JSON content type, and exact
success envelopes. The device requests are saved for later work and are not part
of this automation. Readiness proves connectivity, not migration state; Goose
checks that separately. Unit tests continue to work without PostgreSQL.

Edit this local collection alongside related API changes and review both in Git.
The runner uses a local file and disables run-event reporting. It does not push
collection edits to Postman or configure GitHub Actions. Import the JSON manually
when needed; automatic synchronization with the existing cloud collection is a
separate next step. No Postman API key is required for this local workflow.

```powershell
gofmt -w cmd internal
go mod tidy
go vet ./...
go test ./...
docker compose config --quiet
```

Unit tests require no container. They cover configuration, logging, HTTP contracts,
readiness success/failure and cancellation, and graceful HTTP shutdown. Database
health, migrations, and live readiness must be checked separately using the setup
commands; passing unit tests alone does not prove PostgreSQL connectivity.

References: [pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool),
[Goose](https://pressly.github.io/goose/documentation/cli-commands/), and
[sqlc configuration](https://docs.sqlc.dev/en/latest/reference/config.html).
