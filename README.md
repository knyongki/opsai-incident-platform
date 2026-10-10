# OpsAI

OpsAI is an AI-assisted incident-analysis platform for industrial equipment,
written in Go. Implemented scope: **Milestone 1 (Go API Foundation)** and
**Milestone 2A (PostgreSQL Foundation)**. Device endpoints, incidents, workers,
AI, MQTT, and authentication remain future work. See the
[blueprint](docs/OpsAI-Project-Blueprint.md) and [contributor rules](AGENTS.md).

## Local setup (PowerShell)

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
- `internal/platform/database`: connection pool and startup connectivity check.
- `internal/transport/http`: handlers, envelopes, readiness interface, and server.
- `db/migrations`: reversible Goose SQL migrations.
- `db/sqlc.yaml` and `db/queries`: future query generation configuration.

A pool reuses a bounded set of database connections across requests. This pool
allows 10 connections, a five-minute idle limit, and a one-hour lifetime. HTTP
timeouts remain 5 seconds for headers, 10 seconds for reads and writes, and
60 seconds for idle connections.

The HTTP package defines a small `ReadinessChecker` interface with `Ping(context.Context)`.
Go interfaces are satisfied implicitly: `pgxpool.Pool` and test doubles both provide
that method. HTTP unit tests need no database. `context.Context` carries cancellation
and deadlines; `defer pool.Close()` releases the pool after HTTP shutdown. The
`internal` directory restricts these packages' import scope.

sqlc selects PostgreSQL and `pgx/v5`; the query directory intentionally contains no
SQL, and no code is generated. sqlc is not needed to build or test Milestone 2A.
After adding queries in a future task, use `sqlc generate -f db/sqlc.yaml`.
An empty query directory is not yet a complete generation input.

## Validation

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
