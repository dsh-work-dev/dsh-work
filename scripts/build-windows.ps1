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
    # Subject CN of the code-signing certificate. When it is passed, every
    # update installer must be signed by this publisher; without it the
    # release is distributed unsigned.
    [string]$Publisher,
    [string]$CefSdkRoot,
    # Authenticode-sign the CEF runtime before publication, then the GUI and
    # CLI as -Publisher. The certificate is read
    # from the environment (scripts/windows-signing.ps1).
    [switch]$Sign
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-version.ps1')
. (Join-Path $PSScriptRoot 'windows-signing.ps1')
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$syso = Join-Path $root "wails_windows_$Arch.syso"
$hadSyso = Test-Path -LiteralPath $syso
$backupSyso = Join-Path $env:TEMP "dsh-work-e21-$Arch-$PID.syso"
$backupCreated = $false
$sysoGenerationAttempted = $false
$appPath = Join-Path $root $AppOutput
$cliPath = Join-Path $root $CliOutput
$appDirectory = [IO.Path]::GetFullPath((Split-Path -Parent $appPath))
$cliDirectory = [IO.Path]::GetFullPath((Split-Path -Parent $cliPath))
if ($Arch -eq 'amd64' -and -not [string]::Equals($appDirectory, $cliDirectory, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Windows CEF build requires GUI and CLI outputs in one directory with their shared embedded-cef publication'
}
$release = Resolve-ReleaseVersion -ReleaseVersion $ReleaseVersion
$version = $release.Version
$cefPublication = Join-Path $appDirectory 'embedded-cef'
$wailsMod = & (Join-Path $PSScriptRoot 'prepare-windows-wails.ps1')
$wailsMod = @($wailsMod)[-1]
if (-not (Test-Path -LiteralPath $wailsMod)) { throw 'Pinned Wails composition module was not prepared' }

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

$identity = Resolve-ReleaseIdentity -ReleaseVersion $ReleaseVersion -Prerelease $release.Prerelease -Channel $Channel -FeedRoot $FeedRoot -Publisher $Publisher
if ($Sign -and -not $identity.Publisher) {
    throw 'signing needs a release build with a publisher: pass -ReleaseVersion and -Publisher with -Sign'
}
$identityFlags = ''
if ($identity.Release) {
    $identityFlags = " -X github.com/local/dsh-work/internal/version.BuildChannel=$($identity.Channel)" +
    " -X github.com/local/dsh-work/internal/version.DistributionFeed=$($identity.Feed)"
}
if ($identity.Publisher) {
    # The publisher may contain spaces, so its -X is quoted; Resolve-Publisher
    # has already refused quotes inside it.
    $identityFlags += " '-X=github.com/local/dsh-work/internal/version.DistributionPublisher=$($identity.Publisher)'"
}

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
try {
    Push-Location $root
    New-Item -ItemType Directory -Path $appDirectory -Force | Out-Null
    if ($cliDirectory -ne $appDirectory) { New-Item -ItemType Directory -Path $cliDirectory -Force | Out-Null }
    if ($hadSyso) {
        Copy-Item -LiteralPath $syso -Destination $backupSyso -Force
        $backupCreated = $true
    }
    $cefDigest = ''
    if ($Arch -eq 'amd64') {
        & (Join-Path $PSScriptRoot 'build-windows-cef.ps1') -SdkRoot $CefSdkRoot
        if ($LASTEXITCODE -ne 0) { throw 'Native CEF broker build failed' }
        $cefRoot = Join-Path $root 'bin/windows-cef-build/Release'
        $lock = Get-Content -LiteralPath (Join-Path $root 'internal/embeddedbrowser/distribution/cef-lock.json') -Raw | ConvertFrom-Json
        if ($Sign) {
            $cefBinaries = @($lock.requiredFiles | Where-Object { $_ -match '\.(exe|dll)$' } | ForEach-Object { Join-Path $cefRoot $_ })
            Invoke-AuthenticodeSign -Path $cefBinaries -Publisher $identity.Publisher
        }
        $sdk = if ($CefSdkRoot) { $CefSdkRoot } else { Join-Path $root ('bin/windows-cef-sdk/' + ([string]$lock.sdkArchive -replace '\.tar\.bz2$', '')) }
        $nextPublication = Join-Path $appDirectory "embedded-cef.new-$PID"
        & (Join-Path $PSScriptRoot 'prepare-windows-cef.ps1') -BuildDirectory $cefRoot -SdkRoot $sdk -OutputDirectory $nextPublication -AppVersion $version
        $manifest = Join-Path $nextPublication 'publication.json'
        $stream = [IO.File]::OpenRead($manifest)
        $sha = [Security.Cryptography.SHA256]::Create()
        try { $cefDigest = (($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) -join '') }
        finally { $stream.Dispose(); $sha.Dispose() }
        $outputRoot = $appDirectory.TrimEnd('\','/') + [IO.Path]::DirectorySeparatorChar
        if (-not [IO.Path]::GetFullPath($cefPublication).StartsWith($outputRoot, [StringComparison]::OrdinalIgnoreCase) -or -not [IO.Path]::GetFullPath($nextPublication).StartsWith($outputRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'CEF publication path escaped the requested output directory' }
        if (Test-Path -LiteralPath $cefPublication) {
            $previous = Get-Item -LiteralPath $cefPublication -Force
            if (($previous.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or -not (Test-Path -LiteralPath (Join-Path $cefPublication 'publication.json') -PathType Leaf)) {
                throw 'Existing embedded-cef directory is not an owned publication; refusing to replace it'
            }
            Remove-Item -LiteralPath $cefPublication -Recurse -Force
        }
        Move-Item -LiteralPath $nextPublication -Destination $cefPublication
    }
    $sysoGenerationAttempted = $true
    & wails3 generate syso -arch $Arch -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info build/windows/info.json -out $syso
    if ($LASTEXITCODE -ne 0) { throw "wails3 generate syso failed with exit code $LASTEXITCODE" }

    $cefFlags = if ($cefDigest) { " -X github.com/local/dsh-work/internal/version.CEFPublicationSHA256=$cefDigest" } else { '' }
    $ldflags = "-w -s -H windowsgui -X github.com/local/dsh-work/internal/version.Value=$version$identityFlags$cefFlags"
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    & go build "-modfile=$wailsMod" -tags 'production,devtools' -trimpath -buildvcs=false "-ldflags=$ldflags" -o $appPath .
    if ($LASTEXITCODE -ne 0) { throw "dsh-work production build failed with exit code $LASTEXITCODE" }

    & go build "-modfile=$wailsMod" -tags production -trimpath -buildvcs=false "-ldflags=-w -s -X github.com/local/dsh-work/internal/version.Value=$version$identityFlags$cefFlags" -o $cliPath ./cmd/dsh-work
    if ($LASTEXITCODE -ne 0) { throw "dsh-work CLI production build failed with exit code $LASTEXITCODE" }

    if ($identity.Release) {
        # The linker silently ignores -X for a symbol it cannot find, so a
        # binary can look fine while carrying no identity at all. This checks
        # the bytes that were actually written.
        $identityProblem = Test-BinariesCarryIdentity -AppPath $appPath -CliPath $cliPath -Version $version -Channel $identity.Channel -Feed $identity.Feed -Publisher $identity.Publisher
        if ($identityProblem) { throw $identityProblem }
    }

    if ($Sign) {
        Invoke-AuthenticodeSign -Path @($appPath, $cliPath) -Publisher $identity.Publisher
    }
    if ($Arch -eq 'amd64') {
        & $appPath cef-package-verify
        if ($LASTEXITCODE -ne 0) { throw 'Built application rejected its bound CEF publication' }
    }

    $summary = "version $version, file version $($release.FileVersion)"
    if ($identity.Release) {
        $summary = "$summary, channel $($identity.Channel), feed $($identity.Feed)"
        if ($identity.Publisher) { $summary = "$summary, publisher $($identity.Publisher)" }
    }
    if ($Sign) { $summary = "$summary, signed" }
    Write-Host "built Windows $Arch binaries at $AppOutput and $CliOutput ($summary)"
}
finally {
    Pop-Location
    if ($sysoGenerationAttempted -and (Test-Path -LiteralPath $syso)) { Remove-Item -LiteralPath $syso -Force }
    if ($backupCreated) {
        Copy-Item -LiteralPath $backupSyso -Destination $syso -Force
        Remove-Item -LiteralPath $backupSyso -Force
    }
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
}
