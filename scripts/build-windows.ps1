[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [string]$AppOutput = 'bin/dsh-work.exe',
    [string]$CliOutput = 'bin/dsh-work-cli.exe'
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$syso = Join-Path $root "wails_windows_$Arch.syso"
$hadSyso = Test-Path -LiteralPath $syso
$backupSyso = Join-Path $env:TEMP "dsh-work-e21-$Arch-$PID.syso"
$backupCreated = $false
$appPath = Join-Path $root $AppOutput
$cliPath = Join-Path $root $CliOutput
$versionText = Get-Content -LiteralPath (Join-Path $root 'build/config.yml') -Raw
$versionMatch = [regex]::Match($versionText, '(?m)^\s+version:\s*["'']([^"'']+)["'']\s*$')
if (-not $versionMatch.Success) { throw 'build/config.yml must contain info.version' }
$version = $versionMatch.Groups[1].Value
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw "Windows packaging requires a numeric release version, got $version" }

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
try {
    Push-Location $root
    if ($hadSyso) {
        Copy-Item -LiteralPath $syso -Destination $backupSyso -Force
        $backupCreated = $true
    }
    & wails3 generate syso -arch $Arch -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out $syso
    if ($LASTEXITCODE -ne 0) { throw "wails3 generate syso failed with exit code $LASTEXITCODE" }

    New-Item -ItemType Directory -Path (Split-Path -Parent $appPath) -Force | Out-Null
    $ldflags = "-w -s -H windowsgui -X github.com/local/dsh-work/internal/version.Value=$version"
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    & go build -tags production -trimpath -buildvcs=false "-ldflags=$ldflags" -o $appPath .
    if ($LASTEXITCODE -ne 0) { throw "dsh-work production build failed with exit code $LASTEXITCODE" }

    & go build -tags production -trimpath -buildvcs=false "-ldflags=-w -s -X github.com/local/dsh-work/internal/version.Value=$version" -o $cliPath ./cmd/dsh-work
    if ($LASTEXITCODE -ne 0) { throw "dsh-work CLI production build failed with exit code $LASTEXITCODE" }
    Write-Host "built Windows $Arch binaries at $AppOutput and $CliOutput (version $version)"
}
finally {
    Pop-Location
    if (Test-Path -LiteralPath $syso) { Remove-Item -LiteralPath $syso -Force }
    if ($backupCreated) {
        Copy-Item -LiteralPath $backupSyso -Destination $syso -Force
        Remove-Item -LiteralPath $backupSyso -Force
    }
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
}
