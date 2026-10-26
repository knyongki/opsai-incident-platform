[CmdletBinding()]
param([switch]$MigrateOnly)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'local-environment.ps1')
$previous = @{}
Push-Location $root
try {
    $settings = Read-LocalEnvironment -Root $root
    foreach ($key in $settings.Keys) {
        $previous[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $settings[$key], 'Process')
    }
    if ($MigrateOnly) {
        & go run ./cmd/worker -check-config
        if ($LASTEXITCODE -ne 0) { throw 'Worker configuration validation failed.' }
        foreach ($key in @('GOOSE_DRIVER', 'GOOSE_DBSTRING')) {
            $previous[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        }
        $env:GOOSE_DRIVER = 'postgres'
        $env:GOOSE_DBSTRING = $settings.WORKER_DATABASE_URL
        foreach ($action in @('up', 'status')) {
            $info = [Diagnostics.ProcessStartInfo]::new()
            $info.FileName = Join-Path $root 'bin/goose.exe'
            $info.Arguments = "-env none -timeout 30s -dir db/migrations $action"
            $info.UseShellExecute = $false
            $info.CreateNoWindow = $true
            $info.RedirectStandardOutput = $true
            $info.RedirectStandardError = $true
            $migration = [Diagnostics.Process]::Start($info)
            try {
                # Driver diagnostics may include connection credentials.
                $stdout = $migration.StandardOutput.ReadToEndAsync()
                $stderr = $migration.StandardError.ReadToEndAsync()
                if (-not $migration.WaitForExit(60000)) {
                    $migration.Kill()
                    $migration.WaitForExit()
                    throw "Worker Goose $action timed out."
                }
                $null = $stdout.GetAwaiter().GetResult()
                $null = $stderr.GetAwaiter().GetResult()
                if ($migration.ExitCode -ne 0) { throw "Worker Goose $action failed; check database configuration and incompatible legacy job ownership." }
            } finally { $migration.Dispose() }
            Write-Host "Worker Goose $action succeeded."
        }
        return
    }
    Write-Host 'Starting the explicit fake worker. Its separate test database must already be migrated.'
    & go run ./cmd/worker
    if ($LASTEXITCODE -ne 0) { throw 'Worker stopped with an error.' }
} finally {
    foreach ($key in $previous.Keys) {
        [Environment]::SetEnvironmentVariable($key, $previous[$key], 'Process')
    }
    Pop-Location
}
