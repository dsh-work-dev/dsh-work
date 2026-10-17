$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'resolve-nsis.ps1')

$manifest = Get-Content -Raw (Join-Path $PSScriptRoot '..\toolchain.lock.json') | ConvertFrom-Json
$fixtureManifest = Get-Content -Raw (Join-Path $PSScriptRoot '..\tools\dsh\package.json') | ConvertFrom-Json
$fixtureDeclared = $fixtureManifest.dependencies.'@deepseek-ai/dsh'
# Windows PowerShell's ConvertFrom-Json rejects the lockfile's empty root package key.
$fixtureLocked = (node -p "require(process.argv[1]).packages['node_modules/@deepseek-ai/dsh'].version" (Join-Path $PSScriptRoot '..\tools\dsh\package-lock.json')).Trim()
if ($fixtureDeclared -ne $manifest.dsh.version -or $fixtureLocked -ne $manifest.dsh.version) {
    throw "DSH development fixture mismatch: toolchain.lock.json=$($manifest.dsh.version) tools/dsh/package.json=$fixtureDeclared package-lock=$fixtureLocked"
}

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
$npmMajorMatch = [regex]::Match($npm, '^(?<major>\d+)\.')
if (-not $npmMajorMatch.Success -or [int]$npmMajorMatch.Groups['major'].Value -lt [int]$manifest.packageManager.minimumMajor) {
    throw "npm mismatch: expected >=$($manifest.packageManager.minimumMajor), got $npm"
}
if ($wails -ne $manifest.wails.cli) {
    throw "Wails mismatch: expected $($manifest.wails.cli), got $wails"
}

$makensisPath = if ($env:OS -eq 'Windows_NT') { Resolve-NsisCompiler } else { $null }
if ($env:OS -eq 'Windows_NT' -and -not $makensisPath) {
    throw "NSIS $($manifest.nsis.version) is required on Windows; add NSIS's Bin directory to PATH or install the official package in its standard location"
}
if ($makensisPath) {
    $nsisVersionOutput = (& $makensisPath /VERSION 2>&1 | Out-String).Trim()
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
