[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$destination = Join-Path $repo 'build\WSIT-Client-Windows.exe'
Push-Location $repo
try {
    go build -trimpath -ldflags '-s -w' -o $destination ./cmd/wsitdemo
    if ($LASTEXITCODE -ne 0) { throw "Go build failed: $LASTEXITCODE" }
} finally {
    Pop-Location
}
Get-Item -LiteralPath $destination
