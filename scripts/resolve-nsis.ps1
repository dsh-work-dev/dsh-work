function Resolve-NsisCompiler {
    [CmdletBinding()]
    param()

    $candidates = @()
    $command = Get-Command makensis -ErrorAction SilentlyContinue
    if ($command) {
        $commandPath = if ($command.Path) { $command.Path } else { $command.Source }
        if ($commandPath) { $candidates += $commandPath }
    }

    $programFilesX86 = [Environment]::GetEnvironmentVariable('ProgramFiles(x86)')
    $programFiles = [Environment]::GetEnvironmentVariable('ProgramFiles')
    foreach ($base in @($programFilesX86, $programFiles)) {
        if ($base) { $candidates += (Join-Path $base 'NSIS\Bin\makensis.exe') }
    }

    # The official installer records its location under the 32-bit uninstall
    # view. Include both provider paths so this works in Windows PowerShell
    # and PowerShell 7, regardless of the host process bitness.
    $registryPaths = @(
        'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\NSIS',
        'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\NSIS',
        'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\NSIS'
    )
    foreach ($registryPath in $registryPaths) {
        try {
            $installLocation = (Get-ItemProperty -LiteralPath $registryPath -Name InstallLocation -ErrorAction Stop).InstallLocation
        }
        catch {
            continue
        }
        if ($installLocation) { $candidates += (Join-Path $installLocation 'Bin\makensis.exe') }
    }

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    return $null
}
