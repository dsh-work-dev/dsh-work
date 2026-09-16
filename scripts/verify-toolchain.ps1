$ErrorActionPreference = 'Stop'

$manifest = Get-Content -Raw (Join-Path $PSScriptRoot '..\toolchain.lock.json') | ConvertFrom-Json

$go = (go version).Trim()
$node = (node --version).Trim()
$nodeMajorMatch = [regex]::Match($node, '^v(?<major>\d+)')
$npm = (npm --version).Trim()
$previousErrorActionPreference = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$wailsOutput = ((& cmd.exe /d /c wails3 version 2>&1 | Out-String).Trim())
$ErrorActionPreference = $previousErrorActionPreference
$wails = ([regex]::Match($wailsOutput, 'v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?')).Value

if ($go -notmatch [regex]::Escape("go$($manifest.go.version)")) {
    throw "Go mismatch: expected go$($manifest.go.version), got $go"
}
if (-not $nodeMajorMatch.Success -or [int]$nodeMajorMatch.Groups['major'].Value -lt [int]$manifest.node.minimumMajor) {
    throw "Node mismatch: expected >=$($manifest.node.minimumMajor), got $node"
}
if ($npm -ne $manifest.packageManager.version) {
    throw "npm mismatch: expected $($manifest.packageManager.version), got $npm"
}
if ($wails -ne $manifest.wails.cli) {
    throw "Wails mismatch: expected $($manifest.wails.cli), got $wails"
}

$makensis = Get-Command makensis -ErrorAction SilentlyContinue
if ($env:OS -eq 'Windows_NT' -and -not $makensis) {
    throw "NSIS $($manifest.nsis.version) is required on Windows"
}
if ($makensis) {
    $nsisVersionOutput = (& $makensis.Source /VERSION 2>&1 | Out-String).Trim()
    $nsisVersion = ([regex]::Match($nsisVersionOutput, '(?<!\d)\d+\.\d+(?:\.\d+)?(?!\d)')).Value
    $normalizeNsisVersion = {
        param([string]$value)
        $parts = $value -split '\.'
        if ($parts.Count -eq 2) { return "$value.0" }
        return $value
    }
    $expectedNsisExact = & $normalizeNsisVersion ([string]$manifest.nsis.version)
    $actualNsisExact = if ($nsisVersion) { & $normalizeNsisVersion $nsisVersion } else { '' }
    if ($actualNsisExact -ne $expectedNsisExact) {
        throw "NSIS mismatch: expected $($manifest.nsis.version), got $nsisVersionOutput"
    }
}

Write-Output "toolchain: go=$go node=$node npm=$npm wails=$wails dsh=$($manifest.dsh.version)"
