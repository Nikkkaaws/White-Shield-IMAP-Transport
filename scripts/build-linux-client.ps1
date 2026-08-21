[CmdletBinding()]
param([ValidateSet('amd64', 'arm64')] [string]$Arch = 'amd64')

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$destination = Join-Path $repo "build\WSIT-Client-Linux-$Arch"
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
$env:GOOS = 'linux'
$env:GOARCH = $Arch
Push-Location $repo
try {
    go build -trimpath -ldflags '-s -w' -o $destination ./cmd/wsitdemo
    if ($LASTEXITCODE -ne 0) { throw "Go build failed: $LASTEXITCODE" }
} finally {
    Pop-Location
    if ($null -eq $previousOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousOS }
    if ($null -eq $previousArch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousArch }
}
Get-Item -LiteralPath $destination
