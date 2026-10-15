[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [string]$AppOutput = 'bin/dsh-work.exe',
    [string]$CliOutput = 'bin/dsh-work-cli.exe',
    # Full release version for this build, for example 0.3.0-beta.2. When it is
    # not passed, the plain base version from build/config.yml is used and the
    # build is a local build without release identity.
    [string]$ReleaseVersion,
    # Update channel this release follows: stable, beta or nightly. Releases
    # default to the stage of their own version.
    [string]$Channel,
    # Update feed root this release follows.
    [string]$FeedRoot,
    # Ed25519 public key the release trusts for update payloads. Required for a
    # release build, so a release can never ship without a trust root.
    [string]$PublicKeyFile
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-version.ps1')
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$syso = Join-Path $root "wails_windows_$Arch.syso"
$hadSyso = Test-Path -LiteralPath $syso
$backupSyso = Join-Path $env:TEMP "dsh-work-e21-$Arch-$PID.syso"
$backupCreated = $false
$appPath = Join-Path $root $AppOutput
$cliPath = Join-Path $root $CliOutput
$release = Resolve-ReleaseVersion -ReleaseVersion $ReleaseVersion
$version = $release.Version

# The Windows version resource embedded in the binary comes from
# build/windows/info.json, which scripts/sync-version.mjs writes from the same
# release version. A stale file would ship a binary whose file properties
# disagree with the version it reports, so the build refuses to produce one.
$infoPath = Join-Path $root 'build/windows/info.json'
$infoMetadata = Get-Content -LiteralPath $infoPath -Raw | ConvertFrom-Json
$infoProductVersion = [string]$infoMetadata.info.'0000'.ProductVersion
$infoFileVersion = [string]$infoMetadata.fixed.file_version
if ($infoProductVersion -ne $version -or $infoFileVersion -ne $release.FileVersion) {
    throw "build/windows/info.json carries ProductVersion $infoProductVersion and file version $infoFileVersion, but this build is $version ($($release.FileVersion)). Run: node scripts/sync-version.mjs --release-version $version"
}

$identity = Resolve-ReleaseIdentity -ReleaseVersion $ReleaseVersion -Prerelease $release.Prerelease -Channel $Channel -FeedRoot $FeedRoot -PublicKeyFile $PublicKeyFile
$identityFlags = ''
if ($identity.Release) {
    $identityFlags = " -X github.com/local/dsh-work/internal/version.BuildChannel=$($identity.Channel)" +
    " -X github.com/local/dsh-work/internal/version.DistributionFeed=$($identity.Feed)" +
    " -X github.com/local/dsh-work/internal/version.DistributionPublicKey=$($identity.PublicKeyBase64)"
}

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
    $ldflags = "-w -s -H windowsgui -X github.com/local/dsh-work/internal/version.Value=$version$identityFlags"
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    & go build -tags 'production,devtools' -trimpath -buildvcs=false "-ldflags=$ldflags" -o $appPath .
    if ($LASTEXITCODE -ne 0) { throw "dsh-work production build failed with exit code $LASTEXITCODE" }

    & go build -tags production -trimpath -buildvcs=false "-ldflags=-w -s -X github.com/local/dsh-work/internal/version.Value=$version$identityFlags" -o $cliPath ./cmd/dsh-work
    if ($LASTEXITCODE -ne 0) { throw "dsh-work CLI production build failed with exit code $LASTEXITCODE" }

    if ($identity.Release) {
        # The linker silently ignores -X for a symbol it cannot find, so a
        # binary can look fine while carrying no identity at all. This checks
        # the bytes that were actually written.
        $identityProblem = Test-BinariesCarryIdentity -AppPath $appPath -CliPath $cliPath -Version $version -Channel $identity.Channel -Feed $identity.Feed -PublicKeyBase64 $identity.PublicKeyBase64
        if ($identityProblem) { throw $identityProblem }
    }

    $summary = "version $version, file version $($release.FileVersion)"
    if ($identity.Release) {
        $summary = "$summary, channel $($identity.Channel), feed $($identity.Feed), key $($identity.Fingerprint)"
    }
    Write-Host "built Windows $Arch binaries at $AppOutput and $CliOutput ($summary)"
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
