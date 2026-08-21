[CmdletBinding()]
param(
    [ValidateSet('Debug', 'Release')]
    [string]$Variant = 'Debug',
    [string]$AndroidSdk = "$env:LOCALAPPDATA\Android\Sdk"
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$android = Join-Path $repo 'android'
$gomobile = Join-Path (go env GOPATH) 'bin\gomobile.exe'
$aar = Join-Path $android 'app\libs\wsit-mobile.aar'
$gradle = Join-Path $android 'gradlew.bat'

if (-not (Test-Path -LiteralPath $AndroidSdk)) {
    throw "Android SDK not found: $AndroidSdk"
}
if (-not (Test-Path -LiteralPath $gomobile)) {
    throw "gomobile not found: run go install golang.org/x/mobile/cmd/gomobile@latest"
}
if (-not (Test-Path -LiteralPath $gradle)) {
    throw "Gradle wrapper not found: generate it in android/"
}

$env:ANDROID_HOME = $AndroidSdk
$env:ANDROID_SDK_ROOT = $AndroidSdk
$env:ANDROID_NDK_HOME = Join-Path $AndroidSdk 'ndk\28.2.13676358'

New-Item -ItemType Directory -Force -Path (Split-Path $aar) | Out-Null
Push-Location $repo
try {
    & $gomobile bind -trimpath -target=android/arm64 -androidapi=26 -javapkg=wsit -o $aar ./mobile
    if ($LASTEXITCODE -ne 0) { throw "gomobile bind failed: $LASTEXITCODE" }
} finally {
    Pop-Location
}

$task = if ($Variant -eq 'Release') { ':app:assembleRelease' } else { ':app:assembleDebug' }
Push-Location $android
try {
    & $gradle --no-daemon $task
    if ($LASTEXITCODE -ne 0) { throw "Gradle failed: $LASTEXITCODE" }
} finally {
    Pop-Location
}

$source = if ($Variant -eq 'Release') {
    Join-Path $android 'app\build\outputs\apk\release\app-release-unsigned.apk'
} else {
    Join-Path $android 'app\build\outputs\apk\debug\app-debug.apk'
}
$destination = Join-Path $repo "build\WSIT-Android-$($Variant.ToLowerInvariant()).apk"
New-Item -ItemType Directory -Force -Path (Split-Path $destination) | Out-Null
Copy-Item -LiteralPath $source -Destination $destination -Force
Get-Item $destination
