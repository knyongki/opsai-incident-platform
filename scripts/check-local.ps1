[CmdletBinding()]
param(
    [switch]$ChecksOnly,
    [ValidateRange(1024, 65535)][int]$ApiPort = 18080,
    [ValidateRange(1024, 65535)][int]$DatabasePort = 5432
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$previousEnvironment = @{}
$apiProcess = $null

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program failed (exit $LASTEXITCODE)." }
}

function Invoke-Migration {
    param([string]$Action)
    # Driver errors can include connection details. Never echo Goose output.
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $goose
    $startInfo.Arguments = "-env none -timeout 30s -dir db/migrations $Action"
    $startInfo.WorkingDirectory = $root
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $migration = [Diagnostics.Process]::Start($startInfo)
    try {
        $stdout = $migration.StandardOutput.ReadToEndAsync()
        $stderr = $migration.StandardError.ReadToEndAsync()
        if (-not $migration.WaitForExit(60000)) {
            $migration.Kill()
            $migration.WaitForExit()
            throw "Goose $Action timed out."
        }
        $null = $stdout.GetAwaiter().GetResult()
        $null = $stderr.GetAwaiter().GetResult()
        if ($migration.ExitCode -ne 0) { throw "Goose $Action failed; check local database configuration." }
    } finally { $migration.Dispose() }
    Write-Host "Goose $Action succeeded."
}

Push-Location $root
try {
    # Preserve configured values; use public examples only for missing settings.
    $configuration = @{}
    foreach ($line in Get-Content (Join-Path $root '.env.example')) {
        if ($line -match '^([A-Z_]+)=(.*)$') { $configuration[$Matches[1]] = $Matches[2] }
    }
    foreach ($key in @($configuration.Keys)) {
        $configured = [Environment]::GetEnvironmentVariable($key, 'Process')
        if (-not [string]::IsNullOrWhiteSpace($configured)) { $configuration[$key] = $configured }
    }
    if (-not $ChecksOnly) {
        if ([string]::IsNullOrWhiteSpace($env:DATABASE_URL)) {
            throw 'Set DATABASE_URL in your terminal to your existing local PostgreSQL connection before running. Never paste it into chat or commit it.'
        }
        $databaseUri = $null
        if (-not [Uri]::TryCreate($env:DATABASE_URL, [UriKind]::Absolute, [ref]$databaseUri) -or
            $databaseUri.Scheme -notin @('postgres', 'postgresql') -or
            $databaseUri.Host -notin @('localhost', '127.0.0.1', '[::1]') -or
            ($databaseUri.Port -ne -1 -and $databaseUri.Port -ne $DatabasePort)) {
            throw 'DATABASE_URL must target localhost and match DatabasePort (default 5432).'
        }
    }
    $configuration.HTTP_ADDRESS = "127.0.0.1:$ApiPort"
    $configuration.POSTGRES_PORT = "$DatabasePort"
    $configuration.GOOSE_DRIVER = 'postgres'
    $configuration.GOOSE_DBSTRING = $configuration.DATABASE_URL
    foreach ($key in $configuration.Keys) {
        $previousEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $configuration[$key], 'Process')
    }

    foreach ($command in @('go', 'docker')) { $null = Get-Command $command -ErrorAction Stop }
    Write-Host 'Checking Go formatting, static analysis, unit tests, and Compose configuration...'
    $unformatted = & gofmt -l cmd internal
    if ($LASTEXITCODE -ne 0) { throw 'gofmt failed.' }
    if ($unformatted) { throw 'Go formatting needs attention. Run: gofmt -w cmd internal' }
    Invoke-Checked go @('vet', './...')
    Invoke-Checked go @('test', './...')
    Invoke-Checked docker @('compose', '--env-file', '.env.example', 'config', '--quiet')
    if ($ChecksOnly) {
        Write-Host 'Static and unit checks passed. Live checks were explicitly skipped.'
        return
    }

    $null = Get-Command postman -ErrorAction Stop
    $goose = Join-Path $root 'bin/goose.exe'
    if (-not (Test-Path $goose)) { throw 'Install project-local Goose using the README instructions first.' }
    # Refuse to test an unrelated API that is already listening on this port.
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $ApiPort)
    try { $listener.Start() } finally { $listener.Stop() }

    # Start/reuse the existing service without replacing its configuration.
    Invoke-Checked docker @('compose', '--env-file', '.env.example', 'up', '-d', '--no-recreate', '--wait', '--wait-timeout', '90', 'postgres')
    Invoke-Checked docker @('compose', '--env-file', '.env.example', 'exec', '-T', 'postgres', 'pg_isready')
    Invoke-Migration up
    Invoke-Migration status

    $runId = [Guid]::NewGuid().ToString('N')
    $binary = Join-Path $root "bin/opsai-check-$runId.exe"
    Invoke-Checked go @('build', '-o', $binary, './cmd/api')
    $apiProcess = Start-Process -FilePath $binary -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $root "bin/check-$runId.stdout.log") `
        -RedirectStandardError (Join-Path $root "bin/check-$runId.stderr.log")
    $baseUrl = "http://127.0.0.1:$ApiPort"
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    $ready = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($apiProcess.HasExited) { throw 'The temporary API exited during startup; inspect its ignored bin/check-*.stderr.log.' }
        try {
            $response = Invoke-RestMethod -Uri "$baseUrl/ready" -TimeoutSec 3
            $ready = $response.data.status -eq 'ready'
        } catch {
            # Transient connection/503 errors are expected during bounded startup.
            $ready = $false
        }
        if ($ready) { break }
        Start-Sleep -Milliseconds 300
    }
    if (-not $ready) { throw 'API readiness did not succeed within 30 seconds.' }

    Invoke-Checked postman @('collection', 'run', 'api/postman/opsai.postman_collection.json',
        '-i', 'System', '--env-var', "base_url=$baseUrl", '--no-report-events',
        '--timeout', '30000', '--timeout-request', '5000', '--timeout-script', '5000', '--ignore-redirects')
    Write-Host 'Local validation passed: Go checks, database health, migrations, and Postman System assertions.'
} finally {
    try {
        if ($null -ne $apiProcess) {
            if (-not $apiProcess.HasExited) { Stop-Process -Id $apiProcess.Id -ErrorAction Stop }
            $apiProcess.Dispose()
        }
    } finally {
        foreach ($key in $previousEnvironment.Keys) {
            [Environment]::SetEnvironmentVariable($key, $previousEnvironment[$key], 'Process')
        }
        Pop-Location
    }
}
