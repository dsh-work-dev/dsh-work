# Shared release version resolution for the Windows build and packaging scripts.
#
# The base version stays in build/config.yml. A caller that builds a specific
# release passes -ReleaseVersion, which must be a strict semantic version and
# must agree with the configured base:
#
#   pwsh scripts/build-windows.ps1
#   pwsh scripts/build-windows.ps1 -ReleaseVersion 0.3.0-beta.2
#   pwsh scripts/build-windows.ps1 -ReleaseVersion 0.3.0-nightly.20261013.123456789
#
# The same rules run in internal/release (Go) and scripts/release-version.mjs;
# the copy here keeps a PowerShell-only build honest without needing Node.
# windows/info.json receives the X.Y.Z.0 numeric mapping from sync-version.mjs.
#
# The same file also resolves the release identity a released binary carries:
# the channel that produced it, the update feed root it follows and the Ed25519
# public key it trusts. Those are compile-time values, so they are validated
# here, before any build or packaging work starts.

# Strict semantic version without build metadata: numeric identifiers carry no
# leading zeros, and a pre-release must be a single beta.N or
# nightly.YYYYMMDD.<run id> identifier.
$script:StrictVersionPattern = '^(?<major>0|[1-9][0-9]*)\.(?<minor>0|[1-9][0-9]*)\.(?<patch>0|[1-9][0-9]*)(?:-(?<prerelease>beta\.(?:0|[1-9][0-9]*)|nightly\.[0-9]{8}\.[0-9]+))?$'
$script:MaxVersionField = 65535

function Resolve-ReleaseVersion {
    [CmdletBinding()]
    param(
        [string]$ReleaseVersion,
        [string]$ConfigPath
    )

    if ([string]::IsNullOrWhiteSpace($ConfigPath)) {
        $ConfigPath = Join-Path (Split-Path -Parent $PSScriptRoot) 'build/config.yml'
    }
    if (-not (Test-Path -LiteralPath $ConfigPath)) {
        throw "build/config.yml not found at $ConfigPath"
    }
    $configText = Get-Content -LiteralPath $ConfigPath -Raw
    $baseMatch = [regex]::Match($configText, '(?m)^\s+version:\s*["'']([^"'']+)["'']\s*$')
    if (-not $baseMatch.Success) { throw "build/config.yml must contain info.version" }
    $base = $baseMatch.Groups[1].Value.Trim()
    if ($base -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
        throw "build/config.yml info.version must be a plain X.Y.Z base, got $base"
    }

    $requested = if ([string]::IsNullOrWhiteSpace($ReleaseVersion)) { $base } else { $ReleaseVersion.Trim() }
    if ($requested.StartsWith('v')) { $requested = $requested.Substring(1) }

    $match = [regex]::Match($requested, $script:StrictVersionPattern)
    if (-not $match.Success) {
        throw "release version '$requested' is not a strict semantic version: expected X.Y.Z, X.Y.Z-beta.N or X.Y.Z-nightly.YYYYMMDD.<run id>, with numeric identifiers that have no leading zeros and no build metadata"
    }

    $major = [int]$match.Groups['major'].Value
    $minor = [int]$match.Groups['minor'].Value
    $patch = [int]$match.Groups['patch'].Value
    foreach ($field in @($major, $minor, $patch)) {
        if ($field -gt $script:MaxVersionField) {
            throw "release version '$requested' exceeds the Windows file version limit: every X.Y.Z field must be 0..$($script:MaxVersionField)"
        }
    }

    $resolvedBase = "$major.$minor.$patch"
    if ($resolvedBase -ne $base) {
        throw "release version '$requested' does not belong to base $base from build/config.yml"
    }

    $prerelease = $match.Groups['prerelease'].Value
    if ($prerelease.StartsWith('nightly.')) {
        $stamp = $prerelease.Split('.')[1]
        $parsedStamp = [datetime]::MinValue
        $isDate = [datetime]::TryParseExact(
            $stamp,
            'yyyyMMdd',
            [System.Globalization.CultureInfo]::InvariantCulture,
            [System.Globalization.DateTimeStyles]::None,
            [ref]$parsedStamp)
        if (-not $isDate) {
            throw "release version '$requested' carries $stamp, which is not a real UTC date as YYYYMMDD"
        }
    }

    [pscustomobject]@{
        Version       = $requested
        Base          = $base
        FileVersion   = "$major.$minor.$patch.0"
        Prerelease    = $prerelease
    }
}

# --- release identity -------------------------------------------------------
#
# A released binary carries the channel that produced it, the update feed root
# it follows and the Ed25519 public key it trusts. The scripts substitute those
# three through -ldflags, and they are validated here first so that a malformed
# or accidental identity never reaches an artifact.

$script:ReleaseChannels = @('stable', 'beta', 'nightly')
$script:DefaultFeedRoot = 'https://dsh-work-dev.github.io/dsh-work/updates'
$script:Ed25519PublicKeySize = 32
# PKIX SubjectPublicKeyInfo of an Ed25519 key: the algorithm identifier followed
# by a BIT STRING that holds the 32 raw key bytes.
$script:Ed25519SpkiHeader = [byte[]](0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00)

function Get-Ed25519KeyBlob {
    # Reduces a decoded key blob to the 32 raw Ed25519 public key bytes.
    [CmdletBinding()]
    param([byte[]]$Blob)

    if ($null -eq $Blob -or $Blob.Length -eq 0) { throw 'the public key is empty' }
    if ($Blob.Length -eq $script:Ed25519PublicKeySize) { return , $Blob }

    $spkiLength = $script:Ed25519SpkiHeader.Length + $script:Ed25519PublicKeySize
    if ($Blob.Length -eq $spkiLength) {
        $headerMatches = $true
        for ($index = 0; $index -lt $script:Ed25519SpkiHeader.Length; $index++) {
            if ($Blob[$index] -ne $script:Ed25519SpkiHeader[$index]) { $headerMatches = $false; break }
        }
        if ($headerMatches) {
            $key = New-Object byte[] $script:Ed25519PublicKeySize
            [Array]::Copy($Blob, $script:Ed25519SpkiHeader.Length, $key, 0, $script:Ed25519PublicKeySize)
            return , $key
        }
    }
    throw "the public key is $($Blob.Length) bytes, which is neither 32 raw Ed25519 key bytes nor a $spkiLength-byte PKIX Ed25519 public key"
}

function Get-Ed25519PublicKey {
    # Reads one public key file and returns the 32 raw key bytes. Accepted
    # encodings are the raw key, base64 (padded or not), hex, a PEM public key
    # block and a PKIX SubjectPublicKeyInfo blob, which is what openssl writes.
    [CmdletBinding()]
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) { throw "release public key file not found: $Path" }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "release public key path is not a file: $Path" }
    $bytes = [System.IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -eq 0) { throw "release public key file is empty: $Path" }
    if ($bytes.Length -eq $script:Ed25519PublicKeySize) { return , $bytes }

    $text = ''
    try { $text = [System.Text.Encoding]::UTF8.GetString($bytes).Trim() } catch { $text = '' }

    if ($text.Contains('-----BEGIN')) {
        $block = [regex]::Match($text, '(?s)-----BEGIN [A-Z ]+-----(?<body>.+?)-----END [A-Z ]+-----')
        if (-not $block.Success) { throw "release public key file $Path has an unreadable PEM block; expected a complete -----BEGIN/-----END pair" }
        $der = $null
        try { $der = [Convert]::FromBase64String(($block.Groups['body'].Value -replace '\s', '')) } catch { $der = $null }
        if ($null -eq $der) { throw "release public key file $Path has a PEM block whose body is not valid base64" }
        $pemKey = Get-Ed25519KeyBlob -Blob $der
        return , $pemKey
    }

    $compact = if ($text) { $text -replace '\s', '' } else { '' }
    if ($compact -match '^(0[xX])?(?<hex>[0-9a-fA-F]{64})$') {
        $hex = $Matches['hex']
        $hexKey = New-Object byte[] $script:Ed25519PublicKeySize
        for ($index = 0; $index -lt $script:Ed25519PublicKeySize; $index++) {
            $hexKey[$index] = [Convert]::ToByte($hex.Substring($index * 2, 2), 16)
        }
        return , $hexKey
    }
    if ($compact) {
        $decoded = $null
        try { $decoded = [Convert]::FromBase64String($compact) } catch { $decoded = $null }
        if ($null -eq $decoded) {
            $padding = (4 - ($compact.Length % 4)) % 4
            try { $decoded = [Convert]::FromBase64String($compact + ('=' * $padding)) } catch { $decoded = $null }
        }
        if ($null -ne $decoded) {
            $base64Key = Get-Ed25519KeyBlob -Blob $decoded
            return , $base64Key
        }
    }

    $blobKey = Get-Ed25519KeyBlob -Blob $bytes
    return , $blobKey
}

function Get-PublicKeyFingerprint {
    # Short stable identifier for a key, in the same shape the application
    # reports it (internal/selfupdate.PublicKeyFingerprint): the first eight
    # bytes of the SHA-256 of the raw key, hex encoded after a sha256: label.
    [CmdletBinding()]
    param([byte[]]$Key)

    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash($Key) } finally { $sha.Dispose() }
    $head = ($hash[0..7] | ForEach-Object { $_.ToString('x2') }) -join ''
    return 'sha256:' + $head
}

function Get-BinaryText {
    # Byte-exact view of a binary as text. Latin-1 maps every byte to one
    # character, so a substring search over the result is a byte-sequence
    # search, which is what finds the strings -ldflags substituted.
    [CmdletBinding()]
    param([byte[]]$Bytes)

    return [System.Text.Encoding]::GetEncoding(28591).GetString($Bytes)
}

function Get-TextHit {
    # First occurrence of a value that is not inside the already matched range.
    # "nightly" is part of "0.1.0-nightly.20261013.100000", so a plain search
    # would report a channel that was never compiled into the binary.
    [CmdletBinding()]
    param(
        [string]$Text,
        [string]$Needle,
        [int]$AvoidOffset,
        [int]$AvoidLength
    )

    $start = 0
    while ($true) {
        $index = $Text.IndexOf($Needle, $start, [System.StringComparison]::Ordinal)
        if ($index -lt 0) { return -1 }
        $inside = $AvoidLength -gt 0 -and $index -ge $AvoidOffset -and ($index + $Needle.Length) -le ($AvoidOffset + $AvoidLength)
        if (-not $inside) { return $index }
        $start = $index + 1
    }
}

function Test-BinaryCarriesIdentity {
    # Returns the comma separated names of the identity values that are missing
    # from a built binary, or an empty string when all of them are present.
    #
    # The linker silently ignores -X for an unknown symbol, so a typo in a
    # variable name would otherwise produce a binary without identity and no
    # error at all.
    [CmdletBinding()]
    param(
        [string]$Path,
        [string]$Version,
        [string]$Channel,
        [string]$Feed,
        [string]$PublicKeyBase64
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "missing binary: $Path" }
    $text = Get-BinaryText -Bytes ([System.IO.File]::ReadAllBytes($Path))
    $versionOffset = -1
    if (-not [string]::IsNullOrEmpty($Version)) {
        $versionOffset = $text.IndexOf($Version, [System.StringComparison]::Ordinal)
    }
    $missing = @()
    foreach ($needle in @(
            @{ Name = 'version'; Value = $Version },
            @{ Name = 'channel'; Value = $Channel },
            @{ Name = 'feed'; Value = $Feed },
            @{ Name = 'public key'; Value = $PublicKeyBase64 })) {
        if ([string]::IsNullOrEmpty($needle.Value)) { continue }
        if ($needle.Name -eq 'version') {
            if ($versionOffset -lt 0) { $missing += $needle.Name }
            continue
        }
        $avoidOffset = $versionOffset
        $avoidLength = 0
        if ($versionOffset -ge 0) { $avoidLength = $Version.Length }
        if ((Get-TextHit -Text $text -Needle $needle.Value -AvoidOffset $avoidOffset -AvoidLength $avoidLength) -lt 0) {
            $missing += $needle.Name
        }
    }
    return ($missing -join ', ')
}

function Get-BinaryIdentityRequirement {
    # Which identity each built binary has to carry.
    #
    # Both binaries carry the whole identity. bin/dsh-work.exe is the
    # application that updates itself; bin/dsh-work-cli.exe reports the same
    # identity through its `version` command, and that read-back is also what
    # keeps the three internal/version variables alive for the linker's -X:
    # the linker drops a variable no code references and then silently ignores
    # the -X that names it.
    [CmdletBinding()]
    param(
        [string]$AppPath,
        [string]$CliPath
    )

    return @(
        @{ Label = 'bin/dsh-work.exe'; Path = $AppPath; Identity = $true },
        @{ Label = 'bin/dsh-work-cli.exe'; Path = $CliPath; Identity = $true }
    )
}

function Test-BinariesCarryIdentity {
    # Returns an empty string when every binary carries what it must, otherwise
    # the first problem found.
    [CmdletBinding()]
    param(
        [string]$AppPath,
        [string]$CliPath,
        [string]$Version,
        [string]$Channel,
        [string]$Feed,
        [string]$PublicKeyBase64
    )

    foreach ($binary in (Get-BinaryIdentityRequirement -AppPath $AppPath -CliPath $CliPath)) {
        $binaryChannel = ''
        $binaryFeed = ''
        $binaryKey = ''
        if ($binary.Identity) {
            $binaryChannel = $Channel
            $binaryFeed = $Feed
            $binaryKey = $PublicKeyBase64
        }
        $missing = Test-BinaryCarriesIdentity -Path $binary.Path -Version $Version -Channel $binaryChannel -Feed $binaryFeed -PublicKeyBase64 $binaryKey
        if ($missing) {
            return "$($binary.Label) does not carry the release identity: missing $missing. The linker ignores -X for a symbol it cannot find, so check the internal/version variable names."
        }
    }
    return ''
}

function Resolve-ReleaseIdentity {
    # Resolves the identity a released binary carries.
    #
    # A release build (-ReleaseVersion) must also pin its trust root through
    # -PublicKeyFile. A local build passes no identity parameter at all: it gets
    # no identity, and a local build that passes one by accident is refused
    # instead of silently producing a binary that looks like a release.
    #
    # The channel is metadata that must be stated; when it is not passed it is
    # taken from the release version's own stage, and an explicit channel that
    # disagrees with that stage is refused.
    [CmdletBinding()]
    param(
        [string]$ReleaseVersion,
        [string]$Prerelease,
        [string]$Channel,
        [string]$FeedRoot,
        [string]$PublicKeyFile
    )

    $isRelease = -not [string]::IsNullOrWhiteSpace($ReleaseVersion)
    $identityGiven = (-not [string]::IsNullOrWhiteSpace($Channel)) -or
        (-not [string]::IsNullOrWhiteSpace($FeedRoot)) -or
        (-not [string]::IsNullOrWhiteSpace($PublicKeyFile))
    if (-not $isRelease) {
        if ($identityGiven) {
            throw 'release identity needs an explicit release build: pass -ReleaseVersion <version>, or drop -Channel, -FeedRoot and -PublicKeyFile for a local build'
        }
        return [pscustomobject]@{
            Release         = $false
            Channel         = ''
            Feed            = ''
            PublicKeyBase64 = ''
            Fingerprint     = ''
        }
    }

    if ([string]::IsNullOrWhiteSpace($PublicKeyFile)) {
        throw "release $ReleaseVersion must pin the update trust root: pass -PublicKeyFile <path to the release Ed25519 public key>"
    }

    $expectedChannel = 'stable'
    if ($Prerelease -like 'beta.*') { $expectedChannel = 'beta' }
    elseif ($Prerelease -like 'nightly.*') { $expectedChannel = 'nightly' }
    $resolvedChannel = if ([string]::IsNullOrWhiteSpace($Channel)) { $expectedChannel } else { $Channel.Trim().ToLowerInvariant() }
    if ($script:ReleaseChannels -notcontains $resolvedChannel) {
        throw "channel '$($Channel.Trim())' is not one of stable, beta, nightly"
    }
    if ($resolvedChannel -ne $expectedChannel) {
        throw "channel '$resolvedChannel' does not match release version '$ReleaseVersion': that version is built with -Channel $expectedChannel"
    }

    $resolvedFeed = if ([string]::IsNullOrWhiteSpace($FeedRoot)) { $script:DefaultFeedRoot } else { $FeedRoot.Trim() }
    if ($resolvedFeed -match '[\s"'']|\\') {
        throw "feed root '$resolvedFeed' must not contain whitespace, quotes or backslashes"
    }
    while ($resolvedFeed.EndsWith('/')) { $resolvedFeed = $resolvedFeed.Substring(0, $resolvedFeed.Length - 1) }
    $feedUri = $null
    $isAbsolute = [uri]::TryCreate($resolvedFeed, [System.UriKind]::Absolute, [ref]$feedUri)
    if (-not $isAbsolute -or $feedUri.Scheme -ne 'https' -or [string]::IsNullOrEmpty($feedUri.Host)) {
        throw "feed root '$resolvedFeed' must be an absolute https URL without a trailing slash, for example $($script:DefaultFeedRoot)"
    }
    if ($feedUri.Query -or $feedUri.Fragment) {
        throw "feed root '$resolvedFeed' must not carry a query string or a fragment"
    }

    $keyBytes = Get-Ed25519PublicKey -Path $PublicKeyFile
    $keyBase64 = [Convert]::ToBase64String($keyBytes).TrimEnd('=')
    $fingerprint = Get-PublicKeyFingerprint -Key $keyBytes

    return [pscustomobject]@{
        Release         = $true
        Channel         = $resolvedChannel
        Feed            = $resolvedFeed
        PublicKeyBase64 = $keyBase64
        Fingerprint     = $fingerprint
    }
}
