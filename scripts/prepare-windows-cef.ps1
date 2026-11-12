[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$BuildDirectory,
    [Parameter(Mandatory)][string]$SdkRoot,
    [Parameter(Mandatory)][string]$OutputDirectory,
    [Parameter(Mandatory)][string]$AppVersion
)
$ErrorActionPreference = 'Stop'
$lock = Get-Content -LiteralPath (Join-Path $PSScriptRoot '../internal/embeddedbrowser/distribution/cef-lock.json') -Raw | ConvertFrom-Json
$cefVersion = [string]$lock.cefVersion
$header = Get-Content -LiteralPath (Join-Path $SdkRoot 'include/cef_version.h') -Raw
if (-not $header.Contains($cefVersion)) { throw "CEF SDK version mismatch: expected $cefVersion" }
if (Test-Path -LiteralPath $OutputDirectory) { throw "CEF output directory already exists: $OutputDirectory" }
# The lock is the pinned SDK's CEF_BINARY_FILES/CEF_RESOURCE_FILES plus the
# project's bootstrap/client DLL. Locales and license files have own sources.
$files = @($lock.requiredFiles | Where-Object { $_ -notlike 'locales/*' -and $_ -notin @('LICENSE.txt','CREDITS.html') })
$source = (Resolve-Path -LiteralPath $BuildDirectory).Path
$sdk = (Resolve-Path -LiteralPath $SdkRoot).Path
foreach ($name in $files) {
    if (-not (Test-Path -LiteralPath (Join-Path $source $name) -PathType Leaf)) { throw "CEF build output missing $name" }
}
$locales = Join-Path $source 'locales'
if (-not (Test-Path -LiteralPath (Join-Path $locales 'en-US.pak') -PathType Leaf)) { throw 'CEF build output missing locales/en-US.pak' }
foreach ($name in @('LICENSE.txt','CREDITS.html')) {
    if (-not (Test-Path -LiteralPath (Join-Path $sdk $name) -PathType Leaf)) { throw "CEF SDK missing $name" }
}
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
try {
    $outputRoot = (Resolve-Path -LiteralPath $OutputDirectory).Path.TrimEnd('\', '/')
    foreach ($name in $files) { Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $OutputDirectory $name) }
    Copy-Item -LiteralPath $locales -Destination (Join-Path $OutputDirectory 'locales') -Recurse
    foreach ($name in @('LICENSE.txt','CREDITS.html')) { Copy-Item -LiteralPath (Join-Path $sdk $name) -Destination (Join-Path $OutputDirectory $name) }
    $entries = @(Get-ChildItem -LiteralPath $outputRoot -Recurse -File | Sort-Object FullName | ForEach-Object {
        $relative = $_.FullName.Substring($outputRoot.Length + 1).Replace('\','/')
        $stream = [IO.File]::OpenRead($_.FullName)
        $sha = [Security.Cryptography.SHA256]::Create()
        try { $digest = (($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') }) -join '') }
        finally { $stream.Dispose(); $sha.Dispose() }
        [ordered]@{ path = $relative; size = $_.Length; sha256 = $digest }
    })
    $publication = [ordered]@{ schemaVersion = 1; appVersion = $AppVersion; cefVersion = $cefVersion; architecture = 'amd64'; files = $entries }
    $json = $publication | ConvertTo-Json -Depth 5
    [IO.File]::WriteAllText((Join-Path $outputRoot 'publication.json'), $json, [Text.UTF8Encoding]::new($false))
}
catch {
    Remove-Item -LiteralPath $OutputDirectory -Recurse -Force
    throw
}
