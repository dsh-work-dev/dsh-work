$ErrorActionPreference = 'Stop'

$files = @(rg --files -g '*.go' -g '!tools/**' -g '!.tmp-wails-*/')
if ($LASTEXITCODE -ne 0 -and $files.Count -eq 0) {
    exit 0
}

$unformatted = @()
foreach ($file in $files) {
    $diff = @(gofmt -d $file)
    if ($diff.Count -gt 0) {
        $unformatted += $file
    }
}

if ($unformatted.Count -gt 0) {
    Write-Error ("Unformatted Go files:`n" + ($unformatted -join "`n"))
}
