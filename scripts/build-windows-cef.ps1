[CmdletBinding()]
param([string]$SdkRoot)
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$lock = Get-Content -LiteralPath (Join-Path $root 'internal/embeddedbrowser/distribution/cef-lock.json') -Raw | ConvertFrom-Json
$sdkParent = Join-Path $root 'bin/windows-cef-sdk'
$build = Join-Path $root 'bin/windows-cef-build'
if (-not $SdkRoot) {
    New-Item -ItemType Directory -Path $sdkParent -Force | Out-Null
    $archive = Join-Path $sdkParent ([string]$lock.sdkArchive)
    if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
        $uri = 'https://cef-builds.spotifycdn.com/' + [Uri]::EscapeDataString([string]$lock.sdkArchive)
        & curl.exe --fail --location --retry 3 --output $archive $uri
        if ($LASTEXITCODE -ne 0) { throw 'Pinned CEF SDK download failed' }
    }
    $stream = [IO.File]::OpenRead($archive)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $actual = (($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) -join '') }
    finally { $stream.Dispose(); $sha.Dispose() }
    if ($actual -ne [string]$lock.sdkSHA256) { throw 'Pinned CEF SDK archive digest mismatch' }
    $SdkRoot = Join-Path $sdkParent ([string]$lock.sdkArchive -replace '\.tar\.bz2$', '')
    if (-not (Test-Path -LiteralPath $SdkRoot)) {
        & tar.exe -xjf $archive -C $sdkParent
        if ($LASTEXITCODE -ne 0) { throw 'Pinned CEF SDK extraction failed' }
    }
}
$SdkRoot = (Resolve-Path -LiteralPath $SdkRoot).Path
$header = Get-Content -LiteralPath (Join-Path $SdkRoot 'include/cef_version.h') -Raw
if (-not $header.Contains([string]$lock.cefVersion)) { throw 'Pinned CEF SDK header mismatch' }
$sdkForCmake = $SdkRoot.Replace('\','/')
$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
if (-not (Test-Path -LiteralPath $vswhere)) { throw 'MSVC x64 toolchain is required' }
$major = ([version](& $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationVersion)).Major
$generator = switch ($major) { 17 { 'Visual Studio 17 2022' } 18 { 'Visual Studio 18 2026' } default { throw "Unsupported MSVC major: $major" } }
$cmake = Get-Command cmake -ErrorAction SilentlyContinue
if ($cmake) {
    $cmakeExe = $cmake.Source
    $cmakePrefix = @()
} else {
    $python = Get-Command python -ErrorAction Stop
    $cmakeExe = $python.Source
    $cmakePrefix = @('-m', 'cmake')
    & $cmakeExe @cmakePrefix --version | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'cmake is unavailable on PATH and in the resolved Python environment' }
}
& $cmakeExe @cmakePrefix -S (Join-Path $root 'native/embeddedbrowser') -B $build -G $generator -A x64 "-DCEF_ROOT=$sdkForCmake" -DUSE_SANDBOX=ON
if ($LASTEXITCODE -ne 0) { throw 'Native CEF broker configuration failed' }
& $cmakeExe @cmakePrefix --build $build --config Release --target dsh_cef_broker --parallel 8 -- /nodeReuse:false
if ($LASTEXITCODE -ne 0) { throw 'Native CEF broker build failed' }
$output = Join-Path $build 'Release'
foreach ($name in @($lock.requiredFiles | Where-Object { $_ -notlike 'locales/*' -and $_ -notin @('LICENSE.txt','CREDITS.html') })) {
    if (-not (Test-Path -LiteralPath (Join-Path $output $name) -PathType Leaf)) { throw "Native CEF output missing $name" }
}
if (-not (Test-Path -LiteralPath (Join-Path $output 'locales/en-US.pak') -PathType Leaf)) { throw 'Native CEF output missing en-US locale' }
Write-Host "Native CEF publication inputs: $output and $SdkRoot"
