# Local automation on a new Windows device

This guide sets up the existing PowerShell validation workflow. It uses PostgreSQL
on **127.0.0.1:5432**, a temporary API on **127.0.0.1:18080**, and the local
Postman collection. It runs only the System folder (health and readiness).
Cloud collection synchronization and GitHub Actions are not configured.

## 1. Prepare the tools and project

Install these prerequisites yourself using their official instructions:

| Tool | Requirement |
| --- | --- |
| [Go](https://go.dev/doc/install) | Go 1.26.5 or newer, matching `go.mod` |
| [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) | Running, with Linux containers and Compose |
| [Postman CLI](https://learning.postman.com/docs/postman-cli/postman-cli-installation/) | The `postman` terminal command; this workflow was validated with 1.75.0 |
| Windows PowerShell | The runner uses Windows executables and process handling |

Clone the repository using your repository URL, or copy the project to the new
device. Open PowerShell in its root folder. Replace the path below if needed:

```powershell
cd D:\Yongki\opsai-incident-platform
go version
docker version
docker compose version
postman --version
Test-Path scripts/check-local.ps1
Test-Path api/postman/opsai.postman_collection.json
```

Both `Test-Path` commands must return `True`. Docker must report a server as well
as a client. If a command is missing after installation, open a new terminal.
Ensure your transferred checkout includes the automation files: uncommitted files
from another computer will not appear in a fresh Git clone. Do not transfer `.env`,
credentials, or `bin/`; rebuild local tools. A fresh checkout does not transfer
the old computer's PostgreSQL data. This guide initializes schema, not a data restore.

## 2. Install Goose into the project

Goose applies versioned SQL migrations. Install the already-approved, pinned CLI
into ignored `bin/`, without changing your system PATH:

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

The first installation and run need network access for Go modules and the
PostgreSQL image. sqlc is not required to run these checks because generated Go
files are already included in the project.

## 3. Configure the database in this terminal

Use the following for a new local database named `opsai`, or an existing database
with that name and user. For an existing database, enter its existing password.
For a fresh database, choose a local development password and keep it privately
for future sessions. Adjust the user/database values if your existing setup differs.

Copy this block exactly. Type the password only when the hidden prompt appears:

```powershell
$env:POSTGRES_DB = 'opsai'
$env:POSTGRES_USER = 'opsai'
$env:POSTGRES_PORT = '5432'
$passwordInput = Read-Host 'Enter local PostgreSQL password' -AsSecureString
try {
    $env:POSTGRES_PASSWORD = [System.Net.NetworkCredential]::new('', $passwordInput).Password
    if ([string]::IsNullOrWhiteSpace($env:POSTGRES_PASSWORD)) {
        throw 'The database password must not be empty.'
    }
    $encodedUser = [Uri]::EscapeDataString($env:POSTGRES_USER)
    $encodedPassword = [Uri]::EscapeDataString($env:POSTGRES_PASSWORD)
    $encodedDatabase = [Uri]::EscapeDataString($env:POSTGRES_DB)
    $env:DATABASE_URL = 'postgres://{0}:{1}@127.0.0.1:{2}/{3}?sslmode=disable' -f $encodedUser, $encodedPassword, $env:POSTGRES_PORT, $encodedDatabase
} finally {
    Remove-Variable passwordInput, encodedUser, encodedPassword, encodedDatabase -ErrorAction SilentlyContinue
}
```

The prompt text is just a label: do not replace it with a password or URL.
Enter the password without surrounding quotation marks. URL encoding above
handles special characters such as `@` and `#` automatically. The password is
hidden during input, but the child processes necessarily receive the connection
settings in their environment; this is not persistent encrypted storage.

These variables last only for this terminal and its child processes. Repeat this
step in a new terminal. Do not print `DATABASE_URL` or `POSTGRES_PASSWORD`, put
them in chat, or save real values in tracked files. No `.env` file is required.
`sslmode=disable` here is for the localhost development database only.

The Compose initialization values create a user/database only for an empty
volume. Changing them does not change a password in an existing volume. The
runner reuses existing containers without recreating their configuration.
It expects this repository's Compose service, not a separate native PostgreSQL
installation that happens to occupy port 5432.

## 4. Run the automation

From the same configured terminal:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1
if ($LASTEXITCODE -ne 0) { throw 'Local validation failed; review the output above.' }
```

The policy override applies only to that child process. It does not change the
machine's execution policy. Do not start another API on port 18080 first; the
runner starts and stops its own temporary API.

The runner performs these steps in order:

1. Check Go formatting, run `go vet`, and run database-independent unit tests.
2. Validate Compose and start/reuse PostgreSQL; wait for container health.
3. Apply pending forward migrations and check migration status.
4. Build and start the temporary API; wait for readiness.
5. Run the Postman System folder and fail if requests/assertions fail.
6. Stop the temporary API and restore temporary environment changes.

Forward migrations modify the selected local database schema. The runner does
not run Down migrations, delete volumes, or execute device requests. PostgreSQL
stays running; build files and API logs remain in ignored `bin/`.

## 5. Confirm success

The validated run on the original device produced:

- PostgreSQL healthy and accepting connections on port 5432.
- `Goose up succeeded.` and `Goose status succeeded.`
- Two Postman requests: `/health` and `/ready`, both HTTP 200.
- Seven assertions executed, zero failed.
- `Local validation passed: Go checks, database health, migrations, and Postman System assertions.`

Postman CLI 1.75.0 may print `No authorization data found` and a message about
credentials for publishing run details. In the verified local run these messages
did not prevent execution or success. A Postman login/API key is not needed for
this local-file workflow. Judge success by the assertion summary and exit code,
not by the presence or absence of that message.

This guide has been checked against the runner and the successful original-device
output. A clean-machine installation has not been independently executed.

## 6. Repeat checks and stop safely

In the same configured terminal, repeat the command in step 4. In a new terminal,
open the project folder and repeat step 3 first. Tools need installation only once
per device/checkout, unless versions change.

For formatting, vet, unit tests, and Compose validation only:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-local.ps1 -ChecksOnly
```

This mode needs Go and the Docker CLI, but no configured connection string,
running database, Goose, or Postman. It does not prove live API readiness.

To stop PostgreSQL without deleting its data:

```powershell
docker compose --env-file .env.example stop postgres
```

Closing the terminal clears its environment. The next full validation starts the
database again. Never use `docker compose down -v` or Goose Down as routine cleanup.

## Troubleshooting

| Message or symptom | Action |
| --- | --- |
| `Set DATABASE_URL in your terminal` | Repeat step 3 in the same terminal that launches the runner. Variables from another terminal are not inherited. |
| `Goose up failed` | Verify the user, database, and existing password locally. Container health alone does not verify authentication. Do not delete the volume to fix credentials. |
| Docker daemon unavailable | Start Docker Desktop, wait for its engine, and check `docker version`. |
| Port 5432 already occupied | Check whether another PostgreSQL instance owns it. Preserve it; resolve which database to use before running. |
| Port 18080 already occupied | Use `-ApiPort 18081` on the runner command. |
| Need a different database port | Set `POSTGRES_PORT`, rebuild `DATABASE_URL` with that port, and pass matching `-DatabasePort`. An existing container's published port is not changed by `--no-recreate`. |
| `postman` not recognized or unknown option | Check `postman --version` and `postman collection run --help`; use the official CLI installation instructions above. |
| Local Goose missing | Repeat step 2; `bin/` is intentionally not transferred through Git. |
| Formatting check failed | Run `gofmt -w cmd internal`, review the changes, then rerun. |
| Temporary API exited or readiness timed out | Inspect ignored `bin/check-*.stderr.log` locally; check database credentials and connectivity. Redact any sensitive content before sharing logs. |

The next optional task after a successful setup is reviewing a cloud sync or CI
workflow. Running this script does not update the shared OpsAI API collection.
