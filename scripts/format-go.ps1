$ErrorActionPreference = 'Stop'

$files = @(rg --files -g '*.go' -g '!tools/**' -g '!.tmp-wails-*/')
foreach ($file in $files) {
    gofmt -w $file
}
