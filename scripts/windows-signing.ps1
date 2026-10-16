# Authenticode signing for the Windows release unit.
#
# Dot-sourced by scripts/build-windows.ps1 and scripts/package-windows.ps1 when
# they run with -Sign. The certificate and its password are never command-line
# arguments: they are read from the environment so they do not appear in a
# process list or a log.
#
#   WINDOWS_SIGNING_CERTIFICATE_FILE      path to the PFX holding the code-signing
#                                         certificate and its private key
#   WINDOWS_SIGNING_CERTIFICATE_PASSWORD  password of that PFX
#   WINDOWS_SIGNING_TIMESTAMP_URL         RFC 3161 timestamp server (optional)
#
# Every signed file is read back: the signature must be valid on this machine
# and the signer must be the publisher the release identity names, which is
# the same publisher the update client compares against.

$script:DefaultTimestampUrl = 'http://timestamp.digicert.com'

function Resolve-SignTool {
    # signtool ships with the Windows SDK and is not on PATH by default.
    [CmdletBinding()]
    param()

    $onPath = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    if (Test-Path -LiteralPath $kits) {
        $candidate = Get-ChildItem -LiteralPath $kits -Directory |
            Where-Object { $_.Name -match '^\d+\.\d+\.\d+\.\d+$' } |
            Sort-Object { [version]$_.Name } -Descending |
            ForEach-Object { Join-Path $_.FullName 'x64\signtool.exe' } |
            Where-Object { Test-Path -LiteralPath $_ } |
            Select-Object -First 1
        if ($candidate) { return $candidate }
    }
    throw 'signtool.exe is required to sign a release. Install the Windows SDK signing tools or add signtool to PATH.'
}

function Get-AuthenticodePublisher {
    # Returns the simple display name of the certificate that signed a file,
    # after checking that Windows considers the signature valid. This is the
    # name internal/selfupdate reads with CertGetNameString.
    [CmdletBinding()]
    param([string]$Path)

    Import-Module Microsoft.PowerShell.Security -ErrorAction Stop
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid') {
        throw "$Path does not carry a valid Authenticode signature: $($signature.Status) $($signature.StatusMessage)"
    }
    return $signature.SignerCertificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false)
}

function Assert-AuthenticodePublisher {
    # Refuses a file that is unsigned or signed by anyone but the publisher.
    [CmdletBinding()]
    param(
        [string]$Path,
        [string]$Publisher
    )

    $signer = Get-AuthenticodePublisher -Path $Path
    if ($signer -cne $Publisher) {
        throw "$Path is signed by '$signer', but the release publisher is '$Publisher'. -Publisher must be the subject CN of the signing certificate."
    }
}

function Invoke-AuthenticodeSign {
    # Signs files in place with SHA-256 and an RFC 3161 timestamp, then checks
    # each signature names the publisher.
    [CmdletBinding()]
    param(
        [string[]]$Path,
        [string]$Publisher
    )

    $certificate = $env:WINDOWS_SIGNING_CERTIFICATE_FILE
    if ([string]::IsNullOrWhiteSpace($certificate)) {
        throw 'WINDOWS_SIGNING_CERTIFICATE_FILE must name the PFX of the code-signing certificate'
    }
    if (-not (Test-Path -LiteralPath $certificate -PathType Leaf)) {
        throw "signing certificate not found: $certificate"
    }
    $timestamp = if ([string]::IsNullOrWhiteSpace($env:WINDOWS_SIGNING_TIMESTAMP_URL)) { $script:DefaultTimestampUrl } else { $env:WINDOWS_SIGNING_TIMESTAMP_URL }
    $signtool = Resolve-SignTool

    # The PFX is imported into the user's certificate store for the duration of
    # the signing, so signtool selects it by thumbprint and the password never
    # reaches a command line.
    $password = New-Object System.Security.SecureString
    foreach ($character in [string]$env:WINDOWS_SIGNING_CERTIFICATE_PASSWORD) { $password.AppendChar($character) }
    $password.MakeReadOnly()
    $pfx = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($certificate, $password)
    $thumbprint = $pfx.Thumbprint
    $pfx.Dispose()
    $storePath = "Cert:\CurrentUser\My\$thumbprint"
    $alreadyInstalled = Test-Path -LiteralPath $storePath
    Import-Module PKI -ErrorAction Stop
    Import-PfxCertificate -FilePath $certificate -CertStoreLocation Cert:\CurrentUser\My -Password $password | Out-Null
    try {
        foreach ($file in $Path) {
            & $signtool sign /fd SHA256 /tr $timestamp /td SHA256 /s My /sha1 $thumbprint $file | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "signtool failed to sign $file with exit code $LASTEXITCODE" }
            Assert-AuthenticodePublisher -Path $file -Publisher $Publisher
            Write-Host "signed $file as $Publisher"
        }
    }
    finally {
        if (-not $alreadyInstalled -and (Test-Path -LiteralPath $storePath)) {
            Remove-Item -LiteralPath $storePath -DeleteKey -ErrorAction SilentlyContinue
            if (Test-Path -LiteralPath $storePath) { Remove-Item -LiteralPath $storePath }
        }
    }
}
