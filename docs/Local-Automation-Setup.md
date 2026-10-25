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

Private `.env` values take precedence over inherited terminal values. Missing,
duplicate, or unknown keys fail without printing values. The Go API itself reads
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
running; build artifacts and API logs remain in ignored `bin/`.

Expect two requests, seven assertions, zero failures, and
`Local validation passed`. Local Postman CLI may print cloud-login messages;
the previously verified System run succeeded without login. Check the assertion
summary and exit code. No automatic cloud synchronization or GitHub Actions run.

For checks without services or private configuration:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1 -ChecksOnly
```

This needs Go and Docker CLI, but no database, Goose, or Postman. It uses example
configuration only. Repository integration tests are separate; see the README.
A skipped integration test is not evidence that the transaction works.

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
