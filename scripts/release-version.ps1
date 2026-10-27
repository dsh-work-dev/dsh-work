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
# the channel that produced it, the update feed root it follows and, for a
# signed release, the Authenticode publisher an update installer must be
# signed by. Those are
# compile-time values, so they are validated here, before any build or
# packaging work starts.

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

    # Which base a release version may use is decided once by the preflight
    # (internal/release); the build only takes the version it computed.
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
        Base          = "$major.$minor.$patch"
        FileVersion   = "$major.$minor.$patch.0"
        Prerelease    = $prerelease
    }
}

# --- release identity -------------------------------------------------------
#
# A released binary carries the channel that produced it, the update feed root
# it follows and, when the release is signed, the Authenticode publisher it
# trusts. The scripts substitute those through -ldflags, and they are validated here first so that a
# malformed or accidental identity never reaches an artifact.

$script:ReleaseChannels = @('stable', 'beta', 'nightly')
$script:DefaultFeedRoot = 'https://github.com/dsh-work-dev/dsh-work/releases/download'
$script:MaxPublisherLength = 128

function Resolve-Publisher {
    # Validates the publisher name, which must be the simple display name (the
    # subject CN) of the code-signing certificate. It is passed to the linker
    # inside single quotes, so it may not contain quotes, backslashes or
    # control characters.
    [CmdletBinding()]
    param([string]$Publisher)

    $value = $Publisher.Trim()
    if ($value.Length -eq 0) { throw 'publisher is empty' }
    if ($value.Length -gt $script:MaxPublisherLength) {
        throw "publisher '$value' is longer than $($script:MaxPublisherLength) characters"
    }
    if ($value -match '["''\\`]' -or $value -match '[\x00-\x1f\x7f]') {
        throw "publisher '$value' must not contain quotes, backslashes or control characters"
    }
    return $value
}

function Get-BinaryText {
    # Byte-exact view of a binary as text. Latin-1 maps every byte to one
    # character, so a substring search over the result is a byte-sequence
    # search, which is what finds the strings -ldflags substituted.
    [CmdletBinding()]
    param([byte[]]$Bytes)

    return [System.Text.Encoding]::GetEncoding(28591).GetString($Bytes)
}

function ConvertTo-BinaryNeedle {
    # The byte view of a value as Go stores it: UTF-8 bytes, seen through the
    # same one-byte-per-character mapping as Get-BinaryText, so a publisher
    # name outside ASCII is still found.
    [CmdletBinding()]
    param([string]$Value)

    return Get-BinaryText -Bytes ([System.Text.Encoding]::UTF8.GetBytes($Value))
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
        [string]$Publisher
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
            @{ Name = 'publisher'; Value = $Publisher })) {
        if ([string]::IsNullOrEmpty($needle.Value)) { continue }
        if ($needle.Name -eq 'version') {
            if ($versionOffset -lt 0) { $missing += $needle.Name }
            continue
        }
        $avoidOffset = $versionOffset
        $avoidLength = 0
        if ($versionOffset -ge 0) { $avoidLength = $Version.Length }
        if ((Get-TextHit -Text $text -Needle (ConvertTo-BinaryNeedle -Value $needle.Value) -AvoidOffset $avoidOffset -AvoidLength $avoidLength) -lt 0) {
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
        [string]$Publisher
    )

    foreach ($binary in (Get-BinaryIdentityRequirement -AppPath $AppPath -CliPath $CliPath)) {
        $binaryChannel = ''
        $binaryFeed = ''
        $binaryPublisher = ''
        if ($binary.Identity) {
            $binaryChannel = $Channel
            $binaryFeed = $Feed
            $binaryPublisher = $Publisher
        }
        $missing = Test-BinaryCarriesIdentity -Path $binary.Path -Version $Version -Channel $binaryChannel -Feed $binaryFeed -Publisher $binaryPublisher
        if ($missing) {
            return "$($binary.Label) does not carry the release identity: missing $missing. The linker ignores -X for a symbol it cannot find, so check the internal/version variable names."
        }
    }
    return ''
}

function Resolve-ReleaseIdentity {
    # Resolves the identity a released binary carries.
    #
    # A release build (-ReleaseVersion) may name the publisher its update
    # installers are signed by through -Publisher; without one the release is
    # distributed unsigned and its updates are trusted by their feed. A local
    # build passes no identity parameter at all: it gets
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
        [string]$Publisher
    )

    $isRelease = -not [string]::IsNullOrWhiteSpace($ReleaseVersion)
    $identityGiven = (-not [string]::IsNullOrWhiteSpace($Channel)) -or
        (-not [string]::IsNullOrWhiteSpace($FeedRoot)) -or
        (-not [string]::IsNullOrWhiteSpace($Publisher))
    if (-not $isRelease) {
        if ($identityGiven) {
            throw 'release identity needs an explicit release build: pass -ReleaseVersion <version>, or drop -Channel, -FeedRoot and -Publisher for a local build'
        }
        return [pscustomobject]@{
            Release   = $false
            Channel   = ''
            Feed      = ''
            Publisher = ''
        }
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

    $resolvedPublisher = ''
    if (-not [string]::IsNullOrWhiteSpace($Publisher)) {
        $resolvedPublisher = Resolve-Publisher -Publisher $Publisher
    }

    return [pscustomobject]@{
        Release   = $true
        Channel   = $resolvedChannel
        Feed      = $resolvedFeed
        Publisher = $resolvedPublisher
    }
}
