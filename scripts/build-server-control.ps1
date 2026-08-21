[CmdletBinding()]
param([ValidateSet('amd64', 'arm64')] [string]$Arch = 'amd64')

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$destination = Join-Path $repo "build\WSIT-VPS-Client-linux-$Arch"
$env:GOOS = 'linux'
$env:GOARCH = $Arch
Push-Location $repo
try {
    go build -trimpath -ldflags '-s -w' -o $destination ./cmd/wsitserverctl
    if ($LASTEXITCODE -ne 0) { throw "Go build failed: $LASTEXITCODE" }
} finally {
    Pop-Location
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
}
Get-Item $destination
