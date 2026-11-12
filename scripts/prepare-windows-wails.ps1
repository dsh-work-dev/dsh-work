[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$lock = Get-Content -LiteralPath (Join-Path $root 'toolchain.lock.json') -Raw | ConvertFrom-Json
if ([string]$lock.wails.module -ne 'v3.0.0-beta.16') { throw 'Pinned Wails composition seam needs review' }
Push-Location $root
try {
    $version = & go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3
    if ($LASTEXITCODE -ne 0 -or $version -ne [string]$lock.wails.module) { throw 'Wails module lock mismatch' }
    $module = & go list -m -f '{{.Dir}}' github.com/wailsapp/wails/v3
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $module)) { throw 'Pinned Wails module is unavailable' }
    $copy = Join-Path $root 'bin/windows-wails-patched'
    if (-not (Test-Path -LiteralPath $copy)) { Copy-Item -LiteralPath $module -Destination $copy -Recurse }
    function Patch([string]$relative, [string]$before, [string]$after) {
        $path = Join-Path $copy $relative
        $source = [IO.File]::ReadAllText($path)
        if ($source.Contains($after)) { return }
        if (($source.Split([string[]]@($before), [StringSplitOptions]::None)).Count -ne 2) { throw "Pinned Wails seam drift: $relative" }
        (Get-Item -LiteralPath $path).IsReadOnly = $false
        [IO.File]::WriteAllText($path, $source.Replace($before, $after))
    }
    Patch 'pkg/application/webview_window_options.go' 'WebView2CompositionHosting bool' "WebView2CompositionHosting bool`n`t// Explicit per-window choice; default composition ordering remains unchanged.`n`tWebView2CompositionBehindChildWindows bool"
    Patch 'pkg/application/webview_window_windows.go' 'w.chromium.CompositionControllerEnabled = options.Windows.WebView2CompositionHosting' "w.chromium.CompositionControllerEnabled = options.Windows.WebView2CompositionHosting`n`tw.chromium.CompositionBehindChildWindows = options.Windows.WebView2CompositionBehindChildWindows"
    $chromium = Join-Path $copy 'internal/webview2/pkg/edge/chromium.go'
    $source = [IO.File]::ReadAllText($chromium)
    if (-not $source.Contains('CompositionBehindChildWindows bool')) {
        $pattern = '(?m)^(\s*CompositionControllerEnabled\s+bool[^\r\n]*)'
        if ([regex]::Matches($source, $pattern).Count -ne 1) { throw 'Pinned Chromium field drift' }
        (Get-Item -LiteralPath $chromium).IsReadOnly = $false
        [IO.File]::WriteAllText($chromium, [regex]::Replace($source, $pattern, '$1' + "`n`tCompositionBehindChildWindows bool"))
    }
    Patch 'internal/webview2/pkg/edge/chromium.go' 'newCompositionHost(e.hwnd)' 'newCompositionHost(e.hwnd, !e.CompositionBehindChildWindows)'
    Patch 'internal/webview2/pkg/edge/composition_host.go' 'func newCompositionHost(hwnd uintptr)' 'func newCompositionHost(hwnd uintptr, aboveChildWindows bool)'
    Patch 'internal/webview2/pkg/edge/composition_host.go' 'CreateTargetForHwnd(hwnd, true)' 'CreateTargetForHwnd(hwnd, aboveChildWindows)'
    $mod = Join-Path $root 'bin/windows-wails.mod'
    [IO.File]::WriteAllText($mod, [IO.File]::ReadAllText((Join-Path $root 'go.mod')) + "`nreplace github.com/wailsapp/wails/v3 => " + $copy.Replace('\','/') + "`n")
    Copy-Item -LiteralPath (Join-Path $root 'go.sum') -Destination (Join-Path $root 'bin/windows-wails.sum') -Force
    Write-Output $mod
}
finally { Pop-Location }
