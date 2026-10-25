# Uses the shared startup path without running the test suite or Postman.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'check-local.ps1') -StartApi
