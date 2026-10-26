[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    # Full release version this package ships, for example 0.3.0-nightly.20261013.123456789.
    # When it is not passed, the plain base version from build/config.yml is used.
    [string]$ReleaseVersion,
    # Update channel the packaged binaries follow: stable, beta or nightly.
    # Releases default to the stage of their own version.
    [string]$Channel,
    # Update feed root the packaged binaries follow.
    [string]$FeedRoot,
    # Subject CN of the code-signing certificate the packaged binaries trust,
    # for a signed release. The binaries are checked against it before they are
    # wrapped.
    [string]$Publisher,
    # Require both binaries to be signed by -Publisher and Authenticode-sign
    # the installer before its checksum is written. The certificate is read
    # from the environment (scripts/windows-signing.ps1).
    [switch]$Sign
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'resolve-nsis.ps1')
. (Join-Path $PSScriptRoot 'release-version.ps1')
. (Join-Path $PSScriptRoot 'windows-signing.ps1')
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$nsisDir = Join-Path $root 'build/windows/nsis'
$appPath = Join-Path $root 'bin/dsh-work.exe'
$cliPath = Join-Path $root 'bin/dsh-work-cli.exe'
$release = Resolve-ReleaseVersion -ReleaseVersion $ReleaseVersion
$version = $release.Version
if (-not (Test-Path -LiteralPath $appPath)) { throw "missing application binary: $appPath" }
if (-not (Test-Path -LiteralPath $cliPath)) { throw "missing CLI binary: $cliPath" }

$identity = Resolve-ReleaseIdentity -ReleaseVersion $ReleaseVersion -Prerelease $release.Prerelease -Channel $Channel -FeedRoot $FeedRoot -Publisher $Publisher
if ($Sign -and -not $identity.Publisher) {
    throw 'signing needs a release build with a publisher: pass -ReleaseVersion and -Publisher with -Sign'
}
if ($identity.Release) {
    # The installer wraps the binaries built by scripts/build-windows.ps1. A
    # binary that follows another channel, another feed or another publisher
    # would produce an installer that updates from a source this release does
    # not trust, so packaging refuses it instead of wrapping it.
    $identityProblem = Test-BinariesCarryIdentity -AppPath $appPath -CliPath $cliPath -Version $version -Channel $identity.Channel -Feed $identity.Feed -Publisher $identity.Publisher
    if ($identityProblem) {
        throw "$identityProblem Rebuild the binaries with scripts/build-windows.ps1 passing the same -ReleaseVersion, -Channel, -FeedRoot and -Publisher before packaging."
    }
}
if ($Sign) {
    # A signed installer must not wrap unsigned binaries.
    foreach ($binary in @($appPath, $cliPath)) {
        Assert-AuthenticodePublisher -Path $binary -Publisher $identity.Publisher
    }
}
$toolchain = Get-Content -LiteralPath (Join-Path $root 'toolchain.lock.json') -Raw | ConvertFrom-Json
$expectedNsis = [string]$toolchain.nsis.version
if ($expectedNsis -notmatch '^\d+\.\d+\.\d+$') { throw "toolchain.lock.json must contain a numeric nsis.version" }

$wails = Get-Command wails3 -ErrorAction SilentlyContinue
if (-not $wails) { throw 'wails3 is required to generate the WebView2 bootstrapper' }
$makensisPath = Resolve-NsisCompiler
if (-not $makensisPath) {
    throw "makensis is required to create the Windows installer. Add NSIS's Bin directory to PATH or install the official package in its standard location."
}
$nsisVersionOutput = (& $makensisPath /VERSION 2>&1 | Out-String).Trim()
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
    $nsisToolsPath = Join-Path $nsisDir 'wails_tools.nsh'
    Copy-Item -LiteralPath (Join-Path $generatedRoot 'windows/nsis/wails_tools.nsh') -Destination $nsisToolsPath -Force
    $nsisTools = Get-Content -LiteralPath $nsisToolsPath -Raw
    $nsisTools = $nsisTools.Replace('"DisplayName" "${INFO_PRODUCTNAME}"', '"DisplayName" "${DSH_WORK_DISPLAY_NAME}"')
    $nsisTools = $nsisTools.Replace('"Publisher" "${INFO_COMPANYNAME}"', '"Publisher" "${DSH_WORK_DISPLAY_NAME}"')
    [System.IO.File]::WriteAllText($nsisToolsPath, $nsisTools)
    & wails3 generate webview2bootstrapper -dir $nsisDir
    if ($LASTEXITCODE -ne 0) { throw "WebView2 bootstrapper generation failed with exit code $LASTEXITCODE" }

    $mainDefine = if ($Arch -eq 'amd64') { 'ARG_WAILS_AMD64_BINARY' } else { 'ARG_WAILS_ARM64_BINARY' }
    $cliDefine = if ($Arch -eq 'amd64') { 'ARG_DSH_WORK_CLI_AMD64_BINARY' } else { 'ARG_DSH_WORK_CLI_ARM64_BINARY' }
    # NSIS needs four numeric components for the PE version resource, so the
    # installer carries the numeric mapping while the display metadata and the
    # installer file name keep the full release version.
    $arguments = @(
        '-DWAILS_INSTALL_SCOPE=user',
        '-DREQUEST_EXECUTION_LEVEL=user',
        "-DINFO_FILE_VERSION=$($release.FileVersion)",
        "-DINFO_RELEASE_VERSION=$version",
        "-D$mainDefine=$appPath",
        "-D$cliDefine=$cliPath",
        'project.nsi'
    )
    Push-Location $nsisDir
    try {
        & $makensisPath @arguments
        if ($LASTEXITCODE -ne 0) { throw "makensis failed with exit code $LASTEXITCODE" }
    }
    finally { Pop-Location }
    $installerPath = Join-Path $root "bin/dsh-work-$version-windows-$Arch-installer.exe"
    if (-not (Test-Path -LiteralPath $installerPath)) { throw "makensis reported success but did not create $installerPath" }
    # The checksum below and the feed digest describe the signed bytes, so the
    # installer is signed before either is computed.
    if ($Sign) {
        Invoke-AuthenticodeSign -Path @($installerPath) -Publisher $identity.Publisher
    }
    # go-task can run PowerShell with module autoloading disabled, so the
    # Get-FileHash cmdlet is not guaranteed to be available even though the
    # same script works from an interactive shell. Use the BCL directly so
    # the packaging result is identical in local and CI task environments.
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $stream = [System.IO.File]::OpenRead($installerPath)
        try {
            $hash = (($sha256.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) -join '')
        }
        finally { $stream.Dispose() }
    }
    finally { $sha256.Dispose() }
    "$hash  $([System.IO.Path]::GetFileName($installerPath))" | Set-Content -LiteralPath "$installerPath.sha256" -Encoding ascii
    $summary = "created Windows per-user installer for $Arch (version $version, file version $($release.FileVersion))"
    if ($identity.Release) {
        $summary = "$summary, channel $($identity.Channel), feed $($identity.Feed)"
        if ($identity.Publisher) { $summary = "$summary, publisher $($identity.Publisher)" }
    }
    if ($Sign) { $summary = "$summary, signed" }
    Write-Host $summary
}
finally {
    Pop-Location
    if (Test-Path -LiteralPath $generatedRoot) { Remove-Item -LiteralPath $generatedRoot -Recurse -Force }
}
