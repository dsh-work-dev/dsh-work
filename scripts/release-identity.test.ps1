[CmdletBinding()]
param()

# Checks the release identity rules in scripts/release-version.ps1: which
# identity a build may carry, how each parameter is validated, and how a built
# binary is checked for the identity it was supposed to receive.
#
# Run: powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release-identity.test.ps1
# Prints one line per failure and a summary line; exits non-zero on failure.

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-version.ps1')

$script:Checks = 0
$script:Failures = @()

function Assert-True {
    param([bool]$Condition, [string]$Message)
    $script:Checks++
    if (-not $Condition) { throw "FAILED: $Message" }
}

function Assert-Equal {
    param($Expected, $Actual, [string]$Message)
    $script:Checks++
    if ($Expected -ne $Actual) { throw "FAILED: $Message (expected '$Expected', got '$Actual')" }
}

function Assert-Refused {
    # The action must raise, and the message must explain what to fix.
    param([string]$Pattern, [scriptblock]$Action, [string]$Message)
    $script:Checks++
    $raised = $false
    $text = ''
    try { & $Action | Out-Null } catch { $raised = $true; $text = $_.Exception.Message }
    if (-not $raised) { throw "FAILED: $Message (nothing was refused)" }
    if ($text -notmatch $Pattern) { throw "FAILED: $Message (refused with '$text', which does not match '$Pattern')" }
}

$work = Join-Path $env:TEMP ('dsh-release-identity-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work -Force | Out-Null

function Write-Bytes {
    param([string]$Name, [byte[]]$Bytes)
    $path = Join-Path $work $Name
    [System.IO.File]::WriteAllBytes($path, $Bytes)
    return $path
}

function Write-Text {
    param([string]$Name, [string]$Text)
    $path = Join-Path $work $Name
    [System.IO.File]::WriteAllText($path, $Text)
    return $path
}

try {
    # One fixed key: bytes 0x00..0x1f. Its SHA-256 prefix is what the release
    # output and the operator record are supposed to agree on.
    $key = New-Object byte[] 32
    for ($index = 0; $index -lt 32; $index++) { $key[$index] = [byte]$index }
    $keyBase64 = [Convert]::ToBase64String($key)
    $keyBase64Raw = $keyBase64.TrimEnd('=')
    $keyHash = [System.Security.Cryptography.SHA256]::Create().ComputeHash($key)
    $expectedFingerprint = 'sha256:' + (($keyHash[0..7] | ForEach-Object { $_.ToString('x2') }) -join '')

    $spki = New-Object byte[] 44
    [Array]::Copy($script:Ed25519SpkiHeader, 0, $spki, 0, $script:Ed25519SpkiHeader.Length)
    [Array]::Copy($key, 0, $spki, $script:Ed25519SpkiHeader.Length, 32)

    $rawFile = Write-Bytes 'key.raw' $key
    $base64File = Write-Text 'key.base64' ($keyBase64 + "`r`n")
    $base64RawFile = Write-Text 'key.base64raw' $keyBase64Raw
    $hexFile = Write-Text 'key.hex' (($key | ForEach-Object { $_.ToString('x2') }) -join '')
    $hexUpperFile = Write-Text 'key-upper.hex' ('0x' + (($key | ForEach-Object { $_.ToString('X2') }) -join ''))
    $pemFile = Write-Text 'key.pem' ("-----BEGIN PUBLIC KEY-----`r`n" + [Convert]::ToBase64String($spki) + "`r`n-----END PUBLIC KEY-----`r`n")
    $derFile = Write-Bytes 'key.der' $spki
    $shortFile = Write-Bytes 'key-short.bin' ($key[0..15])
    $emptyFile = Write-Bytes 'key-empty.bin' (New-Object byte[] 0)
    $badPemFile = Write-Text 'key-bad.pem' "-----BEGIN PUBLIC KEY-----`r`nnot base64!!`r`n-----END PUBLIC KEY-----`r`n"
    $unterminatedPemFile = Write-Text 'key-unterminated.pem' "-----BEGIN PUBLIC KEY-----`r`n$keyBase64Raw`r`n"
    $wrongDer = New-Object byte[] 44
    [Array]::Copy($spki, 0, $wrongDer, 0, 44)
    $wrongDer[6] = 0x71
    $wrongDerFile = Write-Bytes 'key-wrong.der' $wrongDer

    $nightly = '0.1.0-nightly.20261013.100000'
    $nightlyPrerelease = 'nightly.20261013.100000'
    $beta = '0.1.0-beta.2'

    # --- accepted identities -------------------------------------------------

    $identity = Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -PublicKeyFile $rawFile
    Assert-True $identity.Release 'a release version produces a release identity'
    Assert-Equal 'nightly' $identity.Channel 'a nightly version defaults to the nightly channel'
    Assert-Equal $script:DefaultFeedRoot $identity.Feed 'the default feed root is the production feed'
    Assert-Equal $keyBase64Raw $identity.PublicKeyBase64 'the injected key is the raw key, base64 without padding'
    Assert-Equal $expectedFingerprint $identity.Fingerprint 'the fingerprint is sha256 of the key bytes'

    $explicit = Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'nightly' -FeedRoot 'https://example.test/updates/' -PublicKeyFile $rawFile
    Assert-Equal 'nightly' $explicit.Channel 'an explicit channel that agrees with the version is accepted'
    Assert-Equal 'https://example.test/updates' $explicit.Feed 'one trailing slash is normalised away'

    $betaIdentity = Resolve-ReleaseIdentity -ReleaseVersion $beta -Prerelease 'beta.2' -PublicKeyFile $base64File
    Assert-Equal 'beta' $betaIdentity.Channel 'a beta version defaults to the beta channel'

    $stableIdentity = Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $hexFile
    Assert-Equal 'stable' $stableIdentity.Channel 'a plain version is a stable release'

    foreach ($variant in @(
            @{ Name = 'padded base64'; File = $base64File },
            @{ Name = 'unpadded base64'; File = $base64RawFile },
            @{ Name = 'hex'; File = $hexFile },
            @{ Name = 'prefixed upper-case hex'; File = $hexUpperFile },
            @{ Name = 'PEM PKIX'; File = $pemFile },
            @{ Name = 'PKIX DER'; File = $derFile })) {
        $parsed = Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $variant.File
        Assert-Equal $keyBase64Raw $parsed.PublicKeyBase64 ("a {0} key file yields the same 32 raw key bytes" -f $variant.Name)
        Assert-Equal $expectedFingerprint $parsed.Fingerprint ("a {0} key file yields the same fingerprint" -f $variant.Name)
    }

    $local = Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease ''
    Assert-True (-not $local.Release) 'a local build has no release identity'
    Assert-Equal '' $local.Channel 'a local build carries no channel'
    Assert-Equal '' $local.PublicKeyBase64 'a local build carries no key'

    # --- refused identities --------------------------------------------------

    Assert-Refused 'needs an explicit release build' { Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease '' -PublicKeyFile $rawFile } 'a local build must not carry a release key'
    Assert-Refused 'needs an explicit release build' { Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease '' -Channel 'beta' } 'a local build must not carry a release channel'
    Assert-Refused 'needs an explicit release build' { Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease '' -FeedRoot 'https://example.test/updates' } 'a local build must not carry a release feed'
    Assert-Refused 'must pin the update trust root' { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease } 'a release without a key is refused'
    Assert-Refused 'must pin the update trust root' { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'nightly' } 'a release with only a channel is still refused'
    Assert-Refused "channel 'stable' does not match release version" { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'stable' -PublicKeyFile $rawFile } 'a nightly release must not be built for the stable channel'
    Assert-Refused "channel 'stable' does not match release version" { Resolve-ReleaseIdentity -ReleaseVersion $beta -Prerelease 'beta.2' -Channel 'stable' -PublicKeyFile $rawFile } 'a beta release must not be built for the stable channel'
    Assert-Refused "channel 'beta' does not match release version" { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'Beta' -PublicKeyFile $rawFile } 'a channel that agrees only in case still has to match the version stage'
    Assert-Refused "channel 'canary' is not one of" { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Channel 'canary' -PublicKeyFile $rawFile } 'an unknown channel is refused'

    $upper = Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'NIGHTLY' -PublicKeyFile $rawFile
    Assert-Equal 'nightly' $upper.Channel 'a channel name is normalised to lower case'

    Assert-Refused 'absolute https URL' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'http://example.test/updates' -PublicKeyFile $rawFile } 'a plain http feed is refused'
    Assert-Refused 'absolute https URL' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'updates' -PublicKeyFile $rawFile } 'a relative feed root is refused'
    Assert-Refused 'must not carry a query string or a fragment' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'https://example.test/updates?x=1' -PublicKeyFile $rawFile } 'a feed root with a query is refused'
    Assert-Refused 'must not contain whitespace, quotes or backslashes' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'https://example.test/up dates' -PublicKeyFile $rawFile } 'a feed root with a space is refused'

    Assert-Refused 'public key file not found' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile (Join-Path $work 'absent.pem') } 'a missing key file is refused'
    Assert-Refused 'public key file is empty' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $emptyFile } 'an empty key file is refused'
    Assert-Refused 'neither 32 raw Ed25519 key bytes nor a 44-byte PKIX Ed25519 public key' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $shortFile } 'a short key blob is refused'
    Assert-Refused 'has a PEM block whose body is not valid base64' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $badPemFile } 'a PEM block with a broken body is refused'
    Assert-Refused 'has an unreadable PEM block' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $unterminatedPemFile } 'an unterminated PEM block is refused'
    Assert-Refused 'neither 32 raw Ed25519 key bytes nor a 44-byte PKIX Ed25519 public key' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $wrongDerFile } 'a PKIX blob that is not an Ed25519 key is refused'
    Assert-Refused 'release public key path is not a file' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -PublicKeyFile $work } 'a directory is refused as key'

    # --- identity read-back --------------------------------------------------

    $plainFile = Write-Text 'plain.bin' ('prefix ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' suffix')
    Assert-Equal 'channel, public key' (Test-BinaryCarriesIdentity -Path $plainFile -Version $nightly -Channel 'stable' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw) 'a binary without identity reports every missing value'

    $fullFile = Write-Text 'full.bin' ('prefix ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' key=' + $keyBase64Raw)
    Assert-Equal '' (Test-BinaryCarriesIdentity -Path $fullFile -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw) 'a binary carrying the identity reports nothing missing'

    $missingVersion = Test-BinaryCarriesIdentity -Path $fullFile -Version '0.1.0-beta.2' -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw
    Assert-Equal 'version' $missingVersion 'a mismatched version is reported missing'

    $versionOnly = Write-Text 'version-only.bin' ('only ' + $nightly + ' here')
    Assert-Equal 'channel, feed, public key' (Test-BinaryCarriesIdentity -Path $versionOnly -Version $nightly -Channel 'nightly' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw) 'the channel is not counted when the only match is inside the version string'
    Assert-Equal '' (Test-BinaryCarriesIdentity -Path $fullFile -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw) 'a channel that stands on its own is counted'

    # --- what each built binary has to carry ---------------------------------

    $requirement = Get-BinaryIdentityRequirement -AppPath 'app.exe' -CliPath 'cli.exe'
    Assert-Equal 2 $requirement.Count 'both built binaries are checked'
    Assert-True ($requirement[0].Identity -and $requirement[0].Label -eq 'bin/dsh-work.exe') 'the application must carry the whole identity'
    Assert-True ($requirement[1].Identity -and $requirement[1].Label -eq 'bin/dsh-work-cli.exe') 'the CLI must carry the whole identity too'

    $fullCli = Write-Text 'full-cli.bin' ('cli ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' key=' + $keyBase64Raw)
    Assert-Equal '' (Test-BinariesCarryIdentity -AppPath $fullFile -CliPath $fullCli -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw) 'both binaries carrying the identity pass'

    $cliOnly = Write-Text 'cli-only.bin' ('cli ' + $nightly + ' build')
    $missingCliIdentity = Test-BinariesCarryIdentity -AppPath $fullFile -CliPath $cliOnly -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw
    Assert-True ($missingCliIdentity -like 'bin/dsh-work-cli.exe does not carry the release identity: missing channel, feed, public key*') ("a CLI without the identity is reported (got '$missingCliIdentity')")

    $problem = Test-BinariesCarryIdentity -AppPath $cliOnly -CliPath $cliOnly -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw
    Assert-True ($problem -like 'bin/dsh-work.exe does not carry the release identity: missing channel, feed, public key*') ("an application without the identity is reported (got '$problem')")

    $missingCliVersion = Test-BinariesCarryIdentity -AppPath $fullFile -CliPath (Write-Text 'cli-empty.bin' 'nothing here') -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -PublicKeyBase64 $keyBase64Raw
    Assert-True ($missingCliVersion -like 'bin/dsh-work-cli.exe does not carry the release identity: missing version*') ("a CLI without its version is reported (got '$missingCliVersion')")

    $bytesFile = Join-Path $work 'bytes.bin'
    [System.IO.File]::WriteAllBytes($bytesFile, [byte[]](0x80, 0x41, 0x42))
    Assert-Equal 3 (Get-BinaryText -Bytes ([System.IO.File]::ReadAllBytes($bytesFile))).Length 'the byte view maps one byte to one character'
    Assert-True ((Get-BinaryText -Bytes ([System.IO.File]::ReadAllBytes($bytesFile))).IndexOf('AB', [System.StringComparison]::Ordinal) -eq 1) 'the byte view finds a byte sequence at its byte offset'
}
catch {
    $script:Failures += $_.Exception.Message
}
finally {
    if (Test-Path -LiteralPath $work) { Remove-Item -LiteralPath $work -Recurse -Force }
}

foreach ($failure in $script:Failures) { Write-Host $failure }
if ($script:Failures.Count -gt 0) {
    Write-Host ("release identity checks: {0} of {1} failed" -f $script:Failures.Count, $script:Checks)
    exit 1
}
Write-Host ("all release-identity checks passed ({0} checks)" -f $script:Checks)
exit 0
