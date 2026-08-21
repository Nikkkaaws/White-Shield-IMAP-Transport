[CmdletBinding()]
param(
    [string]$Apk = '',
    [string]$AndroidSdk = "$env:LOCALAPPDATA\Android\Sdk"
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if ([string]::IsNullOrWhiteSpace($Apk)) {
    $Apk = Join-Path $repoRoot 'build\WSIT-Android-debug.apk'
}
$adb = Join-Path $AndroidSdk 'platform-tools\adb.exe'
if (-not (Test-Path -LiteralPath $adb)) { throw "adb not found: $adb" }
if (-not (Test-Path -LiteralPath $Apk)) { throw "APK not found: $Apk" }

& $adb start-server | Out-Null
$devices = @(& $adb devices | Select-Object -Skip 1 | Where-Object { $_ -match "\tdevice$" })
if ($devices.Count -ne 1) { throw "Expected one authorized Android device, found $($devices.Count)" }
& $adb install -r -t $Apk
if ($LASTEXITCODE -ne 0) { throw "adb install failed: $LASTEXITCODE" }
& $adb shell am start -n io.whiteshield.wsit/.MainActivity
if ($LASTEXITCODE -ne 0) { throw "app launch failed: $LASTEXITCODE" }
