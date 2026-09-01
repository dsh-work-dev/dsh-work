$ErrorActionPreference = 'Stop'

$manifest = Get-Content -Raw (Join-Path $PSScriptRoot '..\toolchain.lock.json') | ConvertFrom-Json

$go = (go version).Trim()
$node = (node --version).Trim()
$npm = (npm --version).Trim()
$previousErrorActionPreference = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$wailsOutput = ((& cmd.exe /d /c wails3 version 2>&1 | Out-String).Trim())
$ErrorActionPreference = $previousErrorActionPreference
$wails = ([regex]::Match($wailsOutput, 'v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?')).Value

if ($go -notmatch [regex]::Escape("go$($manifest.go.version)")) {
    throw "Go mismatch: expected go$($manifest.go.version), got $go"
}
if ($node -ne "v$($manifest.node.version)") {
    throw "Node mismatch: expected v$($manifest.node.version), got $node"
}
if ($npm -ne $manifest.packageManager.version) {
    throw "npm mismatch: expected $($manifest.packageManager.version), got $npm"
}
if ($wails -ne $manifest.wails.cli) {
    throw "Wails mismatch: expected $($manifest.wails.cli), got $wails"
}

Write-Output "toolchain: go=$go node=$node npm=$npm wails=$wails dsh=$($manifest.dsh.version)"
