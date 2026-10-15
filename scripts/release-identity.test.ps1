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
    $publisher = 'Example Software Ltd'
    # A publisher outside ASCII is stored by Go as UTF-8 bytes.
    # Built from code points so this file stays ASCII for Windows PowerShell.
    $widePublisher = -join ([char[]](0x793A, 0x4F8B, 0x8F6F, 0x4EF6))

    $nightly = '0.1.0-nightly.20261013.100000'
    $nightlyPrerelease = 'nightly.20261013.100000'
    $beta = '0.1.0-beta.2'

    # --- accepted identities -------------------------------------------------

    $identity = Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Publisher $publisher
    Assert-True $identity.Release 'a release version produces a release identity'
    Assert-Equal 'nightly' $identity.Channel 'a nightly version defaults to the nightly channel'
    Assert-Equal $script:DefaultFeedRoot $identity.Feed 'the default feed root is the production feed'
    Assert-Equal $publisher $identity.Publisher 'the publisher is carried unchanged'

    $explicit = Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'nightly' -FeedRoot 'https://example.test/updates/' -Publisher $publisher
    Assert-Equal 'nightly' $explicit.Channel 'an explicit channel that agrees with the version is accepted'
    Assert-Equal 'https://example.test/updates' $explicit.Feed 'one trailing slash is normalised away'

    $betaIdentity = Resolve-ReleaseIdentity -ReleaseVersion $beta -Prerelease 'beta.2' -Publisher $publisher
    Assert-Equal 'beta' $betaIdentity.Channel 'a beta version defaults to the beta channel'

    $stableIdentity = Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher "  $publisher  "
    Assert-Equal 'stable' $stableIdentity.Channel 'a plain version is a stable release'
    Assert-Equal $publisher $stableIdentity.Publisher 'surrounding whitespace is trimmed from the publisher'

    $wideIdentity = Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher $widePublisher
    Assert-Equal $widePublisher $wideIdentity.Publisher 'a publisher outside ASCII is accepted'

    $local = Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease ''
    Assert-True (-not $local.Release) 'a local build has no release identity'
    Assert-Equal '' $local.Channel 'a local build carries no channel'
    Assert-Equal '' $local.Publisher 'a local build carries no publisher'

    # --- refused identities --------------------------------------------------

    Assert-Refused 'needs an explicit release build' { Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease '' -Publisher $publisher } 'a local build must not carry a publisher'
    Assert-Refused 'needs an explicit release build' { Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease '' -Channel 'beta' } 'a local build must not carry a release channel'
    Assert-Refused 'needs an explicit release build' { Resolve-ReleaseIdentity -ReleaseVersion '' -Prerelease '' -FeedRoot 'https://example.test/updates' } 'a local build must not carry a release feed'
    Assert-Refused 'must name its installer publisher' { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease } 'a release without a publisher is refused'
    Assert-Refused 'must name its installer publisher' { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'nightly' } 'a release with only a channel is still refused'
    Assert-Refused "channel 'stable' does not match release version" { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'stable' -Publisher $publisher } 'a nightly release must not be built for the stable channel'
    Assert-Refused "channel 'stable' does not match release version" { Resolve-ReleaseIdentity -ReleaseVersion $beta -Prerelease 'beta.2' -Channel 'stable' -Publisher $publisher } 'a beta release must not be built for the stable channel'
    Assert-Refused "channel 'beta' does not match release version" { Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'Beta' -Publisher $publisher } 'a channel that agrees only in case still has to match the version stage'
    Assert-Refused "channel 'canary' is not one of" { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Channel 'canary' -Publisher $publisher } 'an unknown channel is refused'

    $upper = Resolve-ReleaseIdentity -ReleaseVersion $nightly -Prerelease $nightlyPrerelease -Channel 'NIGHTLY' -Publisher $publisher
    Assert-Equal 'nightly' $upper.Channel 'a channel name is normalised to lower case'

    Assert-Refused 'absolute https URL' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'http://example.test/updates' -Publisher $publisher } 'a plain http feed is refused'
    Assert-Refused 'absolute https URL' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'updates' -Publisher $publisher } 'a relative feed root is refused'
    Assert-Refused 'must not carry a query string or a fragment' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'https://example.test/updates?x=1' -Publisher $publisher } 'a feed root with a query is refused'
    Assert-Refused 'must not contain whitespace, quotes or backslashes' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -FeedRoot 'https://example.test/up dates' -Publisher $publisher } 'a feed root with a space is refused'

    Assert-Refused 'must not contain quotes, backslashes or control characters' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher "Example 'Quoted' Ltd" } 'a publisher with a single quote is refused'
    Assert-Refused 'must not contain quotes, backslashes or control characters' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher 'Example "Quoted" Ltd' } 'a publisher with a double quote is refused'
    Assert-Refused 'must not contain quotes, backslashes or control characters' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher 'Example\Ltd' } 'a publisher with a backslash is refused'
    Assert-Refused 'must not contain quotes, backslashes or control characters' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher "Example`tLtd" } 'a publisher with a control character is refused'
    Assert-Refused 'longer than' { Resolve-ReleaseIdentity -ReleaseVersion '0.1.0' -Prerelease '' -Publisher ('x' * 129) } 'an overlong publisher is refused'

    # --- identity read-back --------------------------------------------------

    $plainFile = Write-Text 'plain.bin' ('prefix ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' suffix')
    Assert-Equal 'channel, publisher' (Test-BinaryCarriesIdentity -Path $plainFile -Version $nightly -Channel 'stable' -Feed $script:DefaultFeedRoot -Publisher $publisher) 'a binary without identity reports every missing value'

    $fullFile = Write-Text 'full.bin' ('prefix ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' publisher=' + $publisher)
    Assert-Equal '' (Test-BinaryCarriesIdentity -Path $fullFile -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher) 'a binary carrying the identity reports nothing missing'

    $missingVersion = Test-BinaryCarriesIdentity -Path $fullFile -Version '0.1.0-beta.2' -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher
    Assert-Equal 'version' $missingVersion 'a mismatched version is reported missing'

    $versionOnly = Write-Text 'version-only.bin' ('only ' + $nightly + ' here')
    Assert-Equal 'channel, feed, publisher' (Test-BinaryCarriesIdentity -Path $versionOnly -Version $nightly -Channel 'nightly' -Feed $script:DefaultFeedRoot -Publisher $publisher) 'the channel is not counted when the only match is inside the version string'
    Assert-Equal '' (Test-BinaryCarriesIdentity -Path $fullFile -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher) 'a channel that stands on its own is counted'

    # --- what each built binary has to carry ---------------------------------

    $requirement = Get-BinaryIdentityRequirement -AppPath 'app.exe' -CliPath 'cli.exe'
    Assert-Equal 2 $requirement.Count 'both built binaries are checked'
    Assert-True ($requirement[0].Identity -and $requirement[0].Label -eq 'bin/dsh-work.exe') 'the application must carry the whole identity'
    Assert-True ($requirement[1].Identity -and $requirement[1].Label -eq 'bin/dsh-work-cli.exe') 'the CLI must carry the whole identity too'

    $fullCli = Write-Text 'full-cli.bin' ('cli ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' publisher=' + $publisher)
    Assert-Equal '' (Test-BinariesCarryIdentity -AppPath $fullFile -CliPath $fullCli -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher) 'both binaries carrying the identity pass'

    $cliOnly = Write-Text 'cli-only.bin' ('cli ' + $nightly + ' build')
    $missingCliIdentity = Test-BinariesCarryIdentity -AppPath $fullFile -CliPath $cliOnly -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher
    Assert-True ($missingCliIdentity -like 'bin/dsh-work-cli.exe does not carry the release identity: missing channel, feed, publisher*') ("a CLI without the identity is reported (got '$missingCliIdentity')")

    $problem = Test-BinariesCarryIdentity -AppPath $cliOnly -CliPath $cliOnly -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher
    Assert-True ($problem -like 'bin/dsh-work.exe does not carry the release identity: missing channel, feed, publisher*') ("an application without the identity is reported (got '$problem')")

    $missingCliVersion = Test-BinariesCarryIdentity -AppPath $fullFile -CliPath (Write-Text 'cli-empty.bin' 'nothing here') -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $publisher
    Assert-True ($missingCliVersion -like 'bin/dsh-work-cli.exe does not carry the release identity: missing version*') ("a CLI without its version is reported (got '$missingCliVersion')")

    $wideFile = Join-Path $work 'wide.bin'
    [System.IO.File]::WriteAllBytes($wideFile, [System.Text.Encoding]::UTF8.GetBytes('cli ' + $nightly + ' channel=beta feed=' + $script:DefaultFeedRoot + ' publisher=' + $widePublisher))
    Assert-Equal '' (Test-BinaryCarriesIdentity -Path $wideFile -Version $nightly -Channel 'beta' -Feed $script:DefaultFeedRoot -Publisher $widePublisher) 'a publisher outside ASCII is found as UTF-8 bytes'

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
