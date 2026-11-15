# Set up OpsAI on Windows

This guide prepares a new device for one-command startup and local validation.
Use Windows PowerShell in the project root.

## 1. Prepare the tools

| Tool | Requirement |
| --- | --- |
| [Go](https://go.dev/doc/install) | Go 1.26.5 or newer, matching `go.mod` |
| [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) | Running Linux containers with Compose |
| [Postman CLI](https://learning.postman.com/docs/postman-cli/postman-cli-installation/) | Required for full validation, not API startup; tested with 1.75.0 |
| Windows PowerShell | Used by the startup and validation scripts |

Clone or copy the project, then open a terminal in its root. Confirm the tools:

```powershell
go version
docker version
docker compose version
postman --version
Test-Path scripts/start-local.ps1
Test-Path 'api/postman/OpsAI API/.resources/definition.yaml'
```

Docker must report a server as well as a client. Both file checks must return
True. Open a new terminal after installing missing tools.

A Git clone contains only committed files. It does not transfer private `.env`,
local tools in `bin/`, or PostgreSQL data from another computer. This guide creates
schema on a fresh database; it does not restore old data.

## 2. Install project-local Goose

Goose applies versioned SQL migrations. Install the pinned CLI into ignored
`bin/`, without changing the system PATH:

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
```

The first installation/run needs network access for Go modules and the PostgreSQL
image. sqlc is not needed for startup because its generated Go files are included.

Only when you change SQL in `db/queries` or `db/migrations`, install the pinned
sqlc CLI the same way and regenerate. Never edit `internal/platform/database/dbgen`
by hand:

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

## 3. Configure private .env once

Create it only if it does not exist:

```powershell
if (-not (Test-Path .env)) { Copy-Item .env.example .env }
```

Edit `.env` privately in your editor. Keep every key from `.env.example`, changing
the values to match your local database.

| Settings | What to configure |
| --- | --- |
| `HTTP_ADDRESS` | Normally `127.0.0.1:8080` |
| `POSTGRES_PORT` | Normally `5432` |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | Your local database name, user, and password |
| `DATABASE_URL` | Matching user, password, database, and port on localhost |
| `APP_ENV`, `LOG_LEVEL` | Normally `development` and `INFO` |

Connection URL format (placeholders only):

```text
postgres://USERNAME:PASSWORD@127.0.0.1:5432/DATABASE_NAME?sslmode=disable
```

URL-encode special characters in URL credentials, such as `@` as `%40` and
`#` as `%23`. POSTGRES_PASSWORD keeps the original password. Do not put quotes
around just the password inside the URL. `sslmode=disable` is for localhost only.

The scripts support literal KEY=VALUE lines, blank lines, full-line comments,
and optional quotes around the whole value. There is no interpolation, shell
execution, multiline syntax, or inline-comment syntax. Use complete values,
not references such as `DATABASE_URL=postgres://${POSTGRES_USER}:...`.

Private `.env` values take precedence over inherited terminal values. Missing
required API/database keys, duplicate keys, or unknown keys fail without printing
values. Optional WORKER_/JOB_ keys may be absent from older private files and use
inherited settings or example defaults. Fake mode is disabled by default. The Go API itself reads
environment variables; the scripts load the file into child-process environments.

`.env` stays Git-ignored and contains plaintext local configuration. Never commit,
print, or share it. Keep `.env.example` tracked with safe examples only. Verify:

```powershell
git check-ignore .env
git ls-files -- .env
```

The first command should show `.env`; the second should show nothing.

For an existing database, use its existing credentials. Compose initialization
variables only affect an empty volume. Changing the file does not change an
existing database password. The scripts reuse containers without recreating
their configuration; do not delete volumes to resolve a mismatch.

## 4. Start the project

Start Docker Desktop, then run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-local.ps1
```

Startup validates Compose, starts/reuses PostgreSQL, waits for health, applies
forward migrations, checks migration status, and runs the API. Wait for
**API listening**, then follow the [Postman quickstart](Postman-Quickstart.md).
Keep the terminal open; Ctrl+C stops the API. Startup does not require Postman CLI
and does not run the test suite. The execution-policy override applies only to
this PowerShell process.

## 5. Run validation when needed

Full validation:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
```

It checks Go formatting, runs vet and unit tests, validates Compose, starts/reuses
PostgreSQL, applies forward migrations, and runs the Postman **System** folder
against a temporary API on port 18080. That API stops afterward. PostgreSQL stays
running; build artifacts and API logs remain in ignored `bin/`. Containers are
reused with `--no-recreate`. The database port comes from POSTGRES_PORT unless you
pass `-DatabasePort`; DATABASE_URL must match. The runner installs no tools, uses
only the local collection, and never publishes edits; cloud synchronization needs
an explicit request, and existing collection/request IDs must be preserved.

Expect two requests, nine assertions, zero failures, and
`Local validation passed`. Local Postman CLI may print cloud-login messages;
the previously verified System run succeeded without login. Check the assertion
summary and exit code. No automatic cloud synchronization or GitHub Actions run.

For checks without services or private configuration:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1 -ChecksOnly
```

This needs Go and Docker CLI, but no database, Goose, or Postman. It uses example
configuration only. The runner checks formatting but does not run `gofmt -w` or
`go mod tidy`; after Go changes also run:

```powershell
gofmt -w cmd internal
go mod tidy
go vet ./...
go test ./...
docker compose --env-file .env.example config --quiet
```

Unit tests need no database. A passing System folder or unit test run does not
prove PostgreSQL behavior; use the repository integration tests below.

To include the normal-database repository integration tests in the full run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1 -Integration
```

After migrations it sets `OPSAI_TEST_DATABASE_URL` from the validated localhost
`DATABASE_URL` for this process only, runs the device, incident creation, and
incident read integration tests, and fails on any SKIP. It changes data exactly
as the manual command below does. The job and worker suite stays manual because it
needs the isolated worker database with no workers running. `-Integration`
cannot be combined with `-ChecksOnly` or `-StartApi`.

### Repository integration tests

These tests need the explicit `integration` build tag, a running PostgreSQL with
all migrations applied (including 00004), and `OPSAI_TEST_DATABASE_URL`. Without
the URL they report SKIP, and a skipped test is not evidence that a transaction
works: require PASS. These commands load private `.env` in an isolated process
and print no credentials.

Device, incident creation, and incident read tests against the normal database:

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

The full suite, including job, worker, and migration tests, against the
[isolated worker database](#isolated-fake-worker). Stop all workers first; the job
tests refuse to run while pending or running work exists:

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

With [terminal-only configuration](#alternative-terminal-only-configuration), set
`$env:OPSAI_TEST_DATABASE_URL = $env:DATABASE_URL` and run the same `go test` line.

What the tests do to data: device and incident-read tests use rolled-back
fixtures. The incident creation test commits synthetic records, checks visibility
from another connection, forces a failed job insert to prove rollback, rejects
duplicate jobs, and checks device deletion protection, then deletes only its own
rows. Job and worker tests use committed synthetic fixtures on separate
connections and clean up only their own records; migration compatibility tests use
isolated schemas that are rolled back.

Parser tests, syntax checks, and ChecksOnly have passed. The full runner using
private configuration also passed database health, migrations, readiness, and
System assertions. A clean-device installation has not been independently tested.
See [the task list](Tasks.md) for verification evidence and remaining review work.

## 6. Stop safely

Ctrl+C stops the API. To also stop PostgreSQL while preserving its data:

```powershell
docker compose --env-file .env.example stop postgres
```

Next time, open Docker Desktop and run the startup command again. Your private
file persists, so you do not need to repeat credential setup. Never use
`docker compose down -v` or Goose Down as routine cleanup.

## Alternative: terminal-only configuration

If you choose not to keep a private file, set the matching variables in your
terminal before running the scripts. This setup lasts only for that terminal.
The hidden password input below avoids putting the password in command history:

```powershell
$env:POSTGRES_DB = 'opsai'
$env:POSTGRES_USER = 'opsai'
$env:POSTGRES_PORT = '5432'
$passwordInput = Read-Host 'Enter local PostgreSQL password' -AsSecureString
try {
    $env:POSTGRES_PASSWORD = [System.Net.NetworkCredential]::new('', $passwordInput).Password
    if ([string]::IsNullOrWhiteSpace($env:POSTGRES_PASSWORD)) { throw 'Password must not be empty.' }
    $encodedUser = [Uri]::EscapeDataString($env:POSTGRES_USER)
    $encodedPassword = [Uri]::EscapeDataString($env:POSTGRES_PASSWORD)
    $encodedDatabase = [Uri]::EscapeDataString($env:POSTGRES_DB)
    $env:DATABASE_URL = 'postgres://{0}:{1}@127.0.0.1:{2}/{3}?sslmode=disable' -f $encodedUser, $encodedPassword, $env:POSTGRES_PORT, $encodedDatabase
} finally {
    Remove-Variable passwordInput, encodedUser, encodedPassword, encodedDatabase -ErrorAction SilentlyContinue
}
```

Enter the password at the prompt; do not replace the prompt label with credentials.
If a private `.env` exists, its values still take precedence.

## Troubleshooting

| Symptom | Action |
| --- | --- |
| Missing database configuration | Complete step 3; confirm the file is named `.env`, not `.env.txt`. |
| Invalid/missing/duplicate file key | Match the keys in `.env.example`; use literal values. |
| Goose migration failed | Check existing database credentials and URL encoding locally. Do not delete the volume. |
| Docker daemon unavailable | Start Docker Desktop and check `docker version`. |
| Port 5432 occupied | Identify the existing PostgreSQL instance before changing anything. The scripts expect this repository's Compose service. |
| API port 8080 occupied | Change HTTP_ADDRESS to another localhost port and update Postman's base_url. |
| Validation port 18080 occupied | Pass `-ApiPort 18081` to check-local.ps1. |
| Database port changed | Match POSTGRES_PORT and DATABASE_URL. Existing containers keep their published ports with --no-recreate. |
| Missing Goose or Postman | Install the relevant tool using the steps above. |
| Formatting check failed | Run `gofmt -w cmd internal`, review changes, then rerun. |
| Temporary API exits | Inspect ignored `bin/check-*.stderr.log` locally; redact sensitive content before sharing. |

## Fake worker on the main database

The simplest demo: the worker processes incidents created through the normal API.
Fake results are labeled `source: "fake"` and stay stored permanently, because
each incident holds one result. Use this only with a disposable local development
database.

1. Start the API as usual with `scripts/start-local.ps1`; it applies migrations.
2. Add `WORKER_ANALYZER=fake` and `WORKER_DATABASE_TARGET=main` to private `.env`.
   `WORKER_DATABASE_URL` is not used for this target. Do not run `-MigrateOnly`;
   it refuses the main target.
3. In another terminal, start the worker. It logs a warning that it writes to the
   main database:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-worker.ps1
   ```

4. In Postman, keep `base_url` at your usual API and run **Incidents - Worker
   Result**. The incident goes `open` → `analyzing` → `analyzed` with a stored
   fake analysis. **Incidents - Manual** passes with or without the worker.

Set `WORKER_DATABASE_TARGET` back to `isolated` (or remove it) to return to the
isolated setup below.

## Claude worker

The Claude analyzer produces real structured analyses. Each analysis sends the
incident's title, description, alarm code, logs, and device details to the
Anthropic API and is billed to your account. Use synthetic incidents while
developing. How it works: [Claude analyzer](Worker-and-Analysis.md#claude-analyzer).

1. Get an API key from the Anthropic Console. Add these to private `.env` only,
   never to `.env.example` or chat:

   ```text
   WORKER_ANALYZER=claude
   WORKER_AI_API_KEY=<your key>
   ```

   `WORKER_AI_MODEL` (default `claude-opus-5-5`) and `WORKER_AI_EFFORT` (default
   `high`) are optional. Real analyzers default to a 120s attempt timeout and a 180s
   lease; leave `JOB_ATTEMPT_TIMEOUT` and `JOB_LEASE` empty unless you need longer.
2. Choose the database as for the fake worker: `WORKER_DATABASE_TARGET=main` with
   the normal API, or the isolated setup below with its second API.
3. Start the worker with `scripts/start-worker.ps1`. It first checks the key and
   model with the Models API (no analysis is generated, nothing is billed for the
   check), then logs a warning that incident data is sent to Anthropic.
4. In Postman, run **Incidents - Worker Result**. Its poll waits up to about
   150 seconds, so a Claude analysis has time to finish; the result has
   `source: "claude"`, a `model_name`, and a `prompt_version`. With the Postman CLI,
   pass a long run timeout such as `--timeout 200000`.

If the key, billing, access, model, or effort is rejected, the worker releases the
job back to the queue with its attempt returned and stops with a message naming the
cause, instead of failing incidents; fix the setting and start it again. If an older
`.env` sets `JOB_ATTEMPT_TIMEOUT=30s`, a real analyzer refuses to start: remove it or
set at least `60s`. Set `WORKER_ANALYZER=fake` to go back to free, deterministic
output.

## OpenAI worker

The OpenAI analyzer works like the Claude one, with the same prompt, output
contract, and safety behavior. Each analysis sends the same incident and device
details to the OpenAI API and is billed to your OpenAI account. How it works:
[OpenAI analyzer](Worker-and-Analysis.md#openai-analyzer).

1. Add these to private `.env` only:

   ```text
   WORKER_ANALYZER=openai
   WORKER_AI_API_KEY=<your OpenAI key>
   WORKER_AI_MODEL=<a model that supports the Responses API and structured outputs>
   ```

   `WORKER_AI_MODEL` has no default for OpenAI. `WORKER_AI_EFFORT` (default `high`)
   must be a reasoning effort your model supports.
2. Choose the database and start the worker exactly as for the Claude worker. The
   startup check verifies the key and model with the Models API.
3. Run **Incidents - Worker Result**; the result has `source: "openai"`.

A rejected key, exhausted quota, unknown model, or unsupported effort releases the
job and stops the worker; fix the setting and start it again. Only one analyzer
runs per worker process, and `WORKER_AI_API_KEY` belongs to the chosen provider.

## Isolated fake worker

The API startup command is unchanged. In the default isolated target, the fake
worker is a separate, explicit process and must not consume the normal
development queue. How it processes jobs, its safety rules, and all settings are
described in [Worker and analysis](Worker-and-Analysis.md).

1. Start the existing PostgreSQL container using normal API startup. Create a
   dedicated database once, using the container's configured user:

   ```powershell
   docker compose --env-file .env.example exec -T postgres sh -c 'exec createdb -U "$POSTGRES_USER" opsai_worker_test'
   ```

   If that database already exists, use it only when it contains synthetic demo
   records, or choose a new name ending in `_worker_test`. Do not delete an existing
   database or volume. Database creation requires a role with CREATEDB permission.

2. Add `WORKER_ANALYZER=fake` and `WORKER_DATABASE_URL` to private `.env`.
   Use your existing local credentials and the separate database name in the URL.
   Leave DATABASE_URL pointing to the normal API database. Other worker settings
   are optional; see the [configuration table](Worker-and-Analysis.md#configuration).
   Explicit values in .env override terminal values, including blank values.

3. Apply forward migrations to the separate database, then start the worker:

   Stop every older worker process first and rebuild after pulling changes. Do not
   run different worker versions against the same queue: an older worker can
   complete a job without saving its analysis result.

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-worker.ps1 -MigrateOnly
   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/start-worker.ps1
   ```

   Migration output is suppressed to avoid exposing connection details.
   If migration 00003 reports incompatible legacy job ownership, inspect job state
   locally and review a recovery decision; do not reset rows automatically.

4. Run a second API against that same test database in another terminal. This
   isolated child process overrides only the API's runtime configuration:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -Command {
       $ErrorActionPreference = 'Stop'
       . ./scripts/local-environment.ps1
       $settings = Read-LocalEnvironment -Root (Get-Location).Path
       if ([string]::IsNullOrWhiteSpace($settings.WORKER_DATABASE_URL)) {
           throw 'Configure the separate worker database first.'
       }
       $env:DATABASE_URL = $settings.WORKER_DATABASE_URL
       $env:HTTP_ADDRESS = '127.0.0.1:18082'
       $env:LOG_LEVEL = $settings.LOG_LEVEL
       go run ./cmd/api
       exit $LASTEXITCODE
   }
   ```

5. Set Postman base_url to `http://127.0.0.1:18082`. Run **Incidents - Worker
   Result** with the folder runner: it creates its own device and incident, then
   polls detail at most 20 times (about 500 ms apart) until the job is completed
   and `analysis` holds the stored result with `source: "fake"`. POST still returns
   queued and status `open`; the poll then expects status `analyzed`. Polling repeats only in the runner; a
   single Send checks once. With `WORKER_FAKE_OUTCOME=invalid-output` that folder
   fails by design; instead run Incidents - Manual and expect `analysis_status:
   failed`, status `analysis_failed`, and `analysis: null`. Restore Postman's base_url to your usual API afterward.

For a visible transition, set WORKER_FAKE_DELAY to 5s before starting the worker.
Use transient-once to demonstrate retry then success, permanent for terminal
failure, invalid-output to verify rejected output creates no result, or transient
for attempt exhaustion. Keep delay below JOB_ATTEMPT_TIMEOUT unless
testing timeouts. Restart workers after changing settings; use the same retry and
lease settings in every worker process.

For crash verification, create a synthetic incident, observe running, terminate
only the worker process you launched, and start its replacement. It must wait for
lease expiry, schedule retry, and finish on a later attempt. Ctrl+C is graceful
cancellation and also leaves unfinished claims recoverable, but is not proof of
abrupt-process-crash handling. For competition, start two worker terminals and
submit several incidents; correlate job IDs/attempts in safe logs to check each
lease has one owner. A stale process may still execute after expiry, but cannot
write over a replacement's claim.

Stop all workers before the [repository integration tests](#repository-integration-tests).
They require an empty pending/running queue and clean up only their own fixtures.
Use a fresh dedicated database for tests if a demo left pending work; never empty
the normal development queue for testing. Test databases and demo records remain
until you explicitly choose to remove them. No worker is started by check-local.ps1,
and its automated Postman run remains System-only.
