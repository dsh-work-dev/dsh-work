[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64'
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$nsisDir = Join-Path $root 'build/windows/nsis'
$appPath = Join-Path $root 'bin/dsh-work.exe'
$cliPath = Join-Path $root 'bin/dsh-work-cli.exe'
$versionText = Get-Content -LiteralPath (Join-Path $root 'build/config.yml') -Raw
$versionMatch = [regex]::Match($versionText, '(?m)^\s+version:\s*["'']([^"'']+)["'']\s*$')
if (-not $versionMatch.Success) { throw 'build/config.yml must contain info.version' }
$version = $versionMatch.Groups[1].Value
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw "Windows packaging requires a numeric release version, got $version" }
if (-not (Test-Path -LiteralPath $appPath)) { throw "missing application binary: $appPath" }
if (-not (Test-Path -LiteralPath $cliPath)) { throw "missing CLI binary: $cliPath" }
$toolchain = Get-Content -LiteralPath (Join-Path $root 'toolchain.lock.json') -Raw | ConvertFrom-Json
$expectedNsis = [string]$toolchain.nsis.version
if ($expectedNsis -notmatch '^\d+\.\d+\.\d+$') { throw "toolchain.lock.json must contain a numeric nsis.version" }

$wails = Get-Command wails3 -ErrorAction SilentlyContinue
if (-not $wails) { throw 'wails3 is required to generate the WebView2 bootstrapper' }
$makensis = Get-Command makensis -ErrorAction SilentlyContinue
if (-not $makensis) { throw 'makensis is required to create the Windows installer' }
$nsisVersionOutput = (& $makensis.Source /VERSION 2>&1 | Out-String).Trim()
$nsisVersionMatch = [regex]::Match($nsisVersionOutput, '(?<!\d)\d+\.\d+(?:\.\d+)?(?!\d)')
$normalizeNsisVersion = {
    param([string]$value)
    $parts = $value -split '\.'
    if ($parts.Count -eq 2) { return "$value.0" }
    return $value
}
$expectedNsisExact = & $normalizeNsisVersion $expectedNsis
$actualNsisExact = if ($nsisVersionMatch.Success) { & $normalizeNsisVersion $nsisVersionMatch.Value } else { '' }
if (-not $nsisVersionMatch.Success -or $actualNsisExact -ne $expectedNsisExact) {
    throw "NSIS mismatch: expected $expectedNsis, got $nsisVersionOutput"
}

$generatedRoot = Join-Path $env:TEMP "dsh-work-wails-assets-$PID"
Push-Location $root
try {
    if (Test-Path -LiteralPath $generatedRoot) { Remove-Item -LiteralPath $generatedRoot -Recurse -Force }
    New-Item -ItemType Directory -Path $generatedRoot -Force | Out-Null
    & wails3 update build-assets -name dsh-work -binaryname dsh-work -config build/config.yml -dir $generatedRoot -silent
    if ($LASTEXITCODE -ne 0) { throw "wails3 update build-assets failed with exit code $LASTEXITCODE" }
    Copy-Item -LiteralPath (Join-Path $generatedRoot 'windows/nsis/wails_tools.nsh') -Destination (Join-Path $nsisDir 'wails_tools.nsh') -Force
    & wails3 generate webview2bootstrapper -dir $nsisDir
    if ($LASTEXITCODE -ne 0) { throw "WebView2 bootstrapper generation failed with exit code $LASTEXITCODE" }

    $mainDefine = if ($Arch -eq 'amd64') { 'ARG_WAILS_AMD64_BINARY' } else { 'ARG_WAILS_ARM64_BINARY' }
    $cliDefine = if ($Arch -eq 'amd64') { 'ARG_DSH_WORK_CLI_AMD64_BINARY' } else { 'ARG_DSH_WORK_CLI_ARM64_BINARY' }
    $arguments = @(
        '-DWAILS_INSTALL_SCOPE=user',
        '-DREQUEST_EXECUTION_LEVEL=user',
        "-D$mainDefine=$appPath",
        "-D$cliDefine=$cliPath",
        'project.nsi'
    )
    Push-Location $nsisDir
    try {
        & $makensis.Source @arguments
        if ($LASTEXITCODE -ne 0) { throw "makensis failed with exit code $LASTEXITCODE" }
    }
    finally { Pop-Location }
    $installerPath = Join-Path $root "bin/dsh-work-$version-windows-$Arch-installer.exe"
    if (-not (Test-Path -LiteralPath $installerPath)) { throw "makensis reported success but did not create $installerPath" }
    $hash = (Get-FileHash -LiteralPath $installerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $([System.IO.Path]::GetFileName($installerPath))" | Set-Content -LiteralPath "$installerPath.sha256" -Encoding ascii
    Write-Host "created Windows per-user installer for $Arch"
}
finally {
    Pop-Location
    if (Test-Path -LiteralPath $generatedRoot) { Remove-Item -LiteralPath $generatedRoot -Recurse -Force }
}
