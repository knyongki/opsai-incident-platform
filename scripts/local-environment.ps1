# Parse literal KEY=VALUE settings without executing code or expanding variables.
function Read-LocalEnvironment {
    param([string]$Root, [switch]$ExamplesOnly)
    $settings = @{}
    foreach ($line in Get-Content -LiteralPath (Join-Path $Root '.env.example')) {
        if ($line -match '^([A-Z_][A-Z0-9_]*)=(.*)$') { $settings[$Matches[1]] = $Matches[2] }
    }
    if ($ExamplesOnly) { return $settings }
    foreach ($key in @($settings.Keys)) {
        $value = [Environment]::GetEnvironmentVariable($key, 'Process')
        if ($null -ne $value) { $settings[$key] = $value }
    }
    $path = Join-Path $Root '.env'
    if (Test-Path -LiteralPath $path) {
        $seen = @{}
        $lineNumber = 0
        foreach ($line in Get-Content -LiteralPath $path) {
            $lineNumber++
            $trimmed = $line.Trim()
            if ($trimmed -eq '' -or $trimmed.StartsWith('#')) { continue }
            if ($trimmed -notmatch '^([A-Z_][A-Z0-9_]*)\s*=(.*)$') { throw "Invalid .env syntax at line $lineNumber." }
            $key, $value = $Matches[1], $Matches[2].Trim()
            if (-not $settings.ContainsKey($key) -or $seen.ContainsKey($key)) { throw "Unknown or duplicate .env key at line $lineNumber." }
            $seen[$key] = $true
            if ($value.StartsWith('"') -or $value.StartsWith("'")) {
                if ($value.Length -lt 2 -or $value[$value.Length-1] -ne $value[0]) { throw "Unclosed .env quote at line $lineNumber." }
                $value = $value.Substring(1, $value.Length-2)
            }
            $settings[$key] = $value
        }
        foreach ($key in $settings.Keys) {
            if (-not $seen.ContainsKey($key)) { throw "Missing $key in .env; use the keys from .env.example." }
        }
    } elseif ([string]::IsNullOrWhiteSpace($env:DATABASE_URL)) {
        throw 'Configure private .env using .env.example, or set DATABASE_URL in this terminal.'
    }
    return $settings
}
