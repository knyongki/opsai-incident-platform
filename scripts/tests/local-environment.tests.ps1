$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '../local-environment.ps1')
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('opsai-env-test-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $fixture
$oldURL = $env:DATABASE_URL
$oldWorkerMode = $env:WORKER_ANALYZER
$oldPollInterval = $env:JOB_POLL_INTERVAL
try {
    $env:WORKER_ANALYZER = $null
    $env:JOB_POLL_INTERVAL = $null
    @('DATABASE_URL=example', 'POSTGRES_PORT=5432') | Set-Content -LiteralPath (Join-Path $fixture '.env.example')
    $env:DATABASE_URL = 'inherited'
    $settings = Read-LocalEnvironment -Root $fixture
    if ($settings.DATABASE_URL -ne 'inherited') { throw 'Inherited fallback failed.' }
    @('# comment', 'DATABASE_URL=''literal$NOT_EXPANDED#value''', 'POSTGRES_PORT=5432') | Set-Content -LiteralPath (Join-Path $fixture '.env')
    $settings = Read-LocalEnvironment -Root $fixture
    if ($settings.DATABASE_URL -ne 'literal$NOT_EXPANDED#value') { throw 'Literal parsing or file precedence failed.' }
    if ($env:DATABASE_URL -ne 'inherited') { throw 'Parser modified process environment.' }
    $settings = Read-LocalEnvironment -Root $fixture -ExamplesOnly
    if ($settings.DATABASE_URL -ne 'example') { throw 'ExamplesOnly read private configuration.' }
    Add-Content -LiteralPath (Join-Path $fixture '.env.example') -Value @('WORKER_ANALYZER=', 'JOB_POLL_INTERVAL=1s')
    $settings = Read-LocalEnvironment -Root $fixture
    if ($settings.JOB_POLL_INTERVAL -ne '1s') { throw 'Missing optional worker setting did not default.' }
    if ($settings.WORKER_ANALYZER -ne '') { throw 'Fake worker mode enabled by default.' }
    $env:JOB_POLL_INTERVAL = '2s'
    $settings = Read-LocalEnvironment -Root $fixture
    if ($settings.JOB_POLL_INTERVAL -ne '2s') { throw 'Optional inherited override failed.' }
    Add-Content -LiteralPath (Join-Path $fixture '.env') -Value 'JOB_POLL_INTERVAL=3s'
    $settings = Read-LocalEnvironment -Root $fixture
    if ($settings.JOB_POLL_INTERVAL -ne '3s') { throw 'Optional private value precedence failed.' }
    foreach ($invalid in @(
        @('DATABASE_URL=one', 'DATABASE_URL=two', 'POSTGRES_PORT=5432'),
        @('DATABASE_URL=one'),
        @('DATABASE_URL=one', 'POSTGRES_PORT=5432', 'UNKNOWN=hidden'),
        @('DATABASE_URL="hidden', 'POSTGRES_PORT=5432'),
        @('hidden malformed line', 'POSTGRES_PORT=5432')
    )) {
        $invalid | Set-Content -LiteralPath (Join-Path $fixture '.env')
        $failed = $false
        try { $null = Read-LocalEnvironment -Root $fixture } catch {
            $failed = $true
            if ($_.Exception.Message.Contains('hidden')) { throw 'Error exposed input.' }
        }
        if (-not $failed) { throw 'Invalid file accepted.' }
    }
    Write-Host 'Local environment parser checks passed.'
} finally {
    $env:DATABASE_URL = $oldURL
    $env:WORKER_ANALYZER = $oldWorkerMode
    $env:JOB_POLL_INTERVAL = $oldPollInterval
    # Delete only the two synthetic files created in this unique test directory.
    Remove-Item -LiteralPath (Join-Path $fixture '.env'), (Join-Path $fixture '.env.example')
    Remove-Item -LiteralPath $fixture
}
