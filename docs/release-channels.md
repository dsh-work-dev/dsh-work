# Release channels, update feeds and promotion

dsh-work ships three release channels. Each channel has one version shape, one
Windows installer, one GitHub Release, and one update feed document that a
released build reads at runtime.

This document is the operating contract for the release pipeline in
`.github/workflows/`. It records what the pipeline guarantees, what it refuses,
and what a channel needs before it can publish.

## Channels

| Channel | Version shape | Published from | Publish trigger |
| --- | --- | --- | --- |
| stable | `X.Y.Z` | the release tag's commit | push tag `vX.Y.Z` |
| beta | `X.Y.Z-beta.N` | the release tag's commit | push tag `vX.Y.Z-beta.N` |
| nightly | `X.Y.Z-nightly.YYYYMMDD.<run id>` | `main`, or the default branch | daily schedule when `main` moved, manual dispatch |

`X.Y.Z` is the base version in `build/config.yml` (`info.version`). A release
version never invents a base: `scripts/release-version.ps1` and
`internal/release` both refuse a version that does not belong to the configured
base, and a nightly is refused when its base is not strictly higher than the
highest published stable version. Raising the stable line is therefore an edit
to `build/config.yml`, made deliberately, before the nightly that needs it.

The three channels do not share precedence. SemVer orders
`X.Y.Z-nightly.*` above `X.Y.Z-beta.N` for the same base, because `beta` sorts
before `nightly`, so channel priority is never derived from `Compare`. A build
knows which channel it belongs to because the channel is compiled into it, and
a feed only ever serves its own channel.

## Release identity

One release identity is computed once per run by
`node scripts/release-version.mjs` and carried through every later step. The
identity is `{version, channel, base, sourceSha, date, runId, fileVersion}`;
`fileVersion` is the four-field Windows file version derived from the base
(`X.Y.Z.0`), and `runId` is the workflow run id, never the run attempt, so a
retry of the same run reproduces the same nightly version instead of publishing
a second identity for the same commit.

`scripts/sync-version.mjs --release-version <printed version>` writes that
identity into `build/windows/info.json`, the Windows manifest, the macOS plists
and the nfpm package. The build script refuses to build when
`build/windows/info.json` does not already carry the version it was asked for,
so the sync step cannot be skipped.

A released Windows binary also carries three linker-injected values from
`internal/version`:

| Variable | Compiled value |
| --- | --- |
| `Value` | the full release version |
| `BuildChannel` | the channel the build follows |
| `DistributionFeed` | the feed root |
| `DistributionPublisher` | for a signed release, the Authenticode publisher an update installer must be signed by; empty for an unsigned release |

`scripts/build-windows.ps1` refuses a release build that does not resolve a
channel, takes the publisher from `-Publisher` (the subject CN of the
code-signing certificate) when the release is signed, and verifies after
linking that the binary really carries the identity (the linker silently ignores `-X` for a symbol it cannot find). A local
build passes no identity at all and stays non-updatable on purpose.

## Update trust

dsh-work is distributed through GitHub without a code-signing certificate. An
unsigned release trusts an update because it came from the feed compiled into
the build, over HTTPS, from the project's GitHub Releases.
Before the installer runs, `internal/selfupdate` requires:

- the downloaded bytes match the feed's SHA-512 digest and size, so a truncated
  or corrupted download is refused;
- the version strings inside the installer name the release the feed
  advertised, so a feed cannot relabel an older installer as a newer release;
- when the build names a publisher (`DistributionPublisher`), the installer's
  embedded Authenticode signature chains to a root Windows trusts
  (`WinVerifyTrust`, revocation from cache only) and its signer's simple
  display name (normally the subject CN) equals that publisher exactly.

The publisher check switches on by configuration alone: once
`WINDOWS_SIGNING_CERTIFICATE` and `WINDOWS_PUBLISHER` are set, the next release
is signed and compiled with the publisher, and every installation of it then
refuses unsigned updates and updates from any other signer. Nothing at runtime
can turn that off. A development build skips the signature check so a locally
built installer can exercise the handoff from a local feed.

What this costs: an unsigned release can be updated by anyone who can push to
the repository's Releases, so the GitHub account is the
trust root. That is the same account that serves the installer to new users, so
the update path adds no weaker link. A first install from the browser shows
SmartScreen's "unknown publisher" prompt; updates do not, because the
application downloads the installer itself and installs it per user without
elevation.

Rejected: buying a code-signing certificate now, because the only gain is the
first-install prompt and the project has no distribution need that requires it;
free signing for open-source projects (SignPath Foundation) remains an option.
Also rejected: a separate Ed25519 update-signing key over the manifest (the
Wails updater's scheme), which would keep the GitHub account from being enough
to push an update but adds a key to create, store and rotate that the project
decided not to operate. Trusting any valid Authenticode signature without the
publisher check is rejected for signed releases, because every signed
installer on the internet would pass.

Revisit when: the project gets a code-signing certificate (configure it, no code
change); the GitHub account or repository is shared with people who should not
be able to ship updates; a platform other than Windows gets an updater; or the
certificate is renewed under a different subject name (installations on the old
name can only move to it through a release signed with the old certificate).

## Feeds

Every channel publishes exactly one JSON document, the `update.json` asset of a
fixed release tagged `updater-<channel>`:

```
https://github.com/dsh-work-dev/dsh-work/releases/download/updater-stable/update.json
https://github.com/dsh-work-dev/dsh-work/releases/download/updater-beta/update.json
https://github.com/dsh-work-dev/dsh-work/releases/download/updater-nightly/update.json
```

The feed root compiled into a build is
`https://github.com/dsh-work-dev/dsh-work/releases/download`, and
`internal/desktopapp/update.go` builds `<feed root>/updater-<channel>/update.json`.
A build with no feed root logs that updates stay unavailable rather than
falling back to a default. The `updater-*` releases are prereleases that are
never marked latest, so they cannot become the repository's latest release or
the stable gate a nightly is checked against.

The feed is kept apart from any website on purpose: a website can be built and
deployed in any way without being able to remove or roll back the feed, and the
pipeline needs nothing but the repository's Releases. A download mirror can be
placed in front of the same URLs later.

Rejected: serving the feeds from GitHub Pages (`gh-pages` branch). It makes a
release depend on Pages being enabled and makes any future website deployment
that rewrites the branch a way to delete the feed. Also rejected: deriving the
latest release per channel from the Releases API at check time, which needs
API calls (rate-limited without a token) and release-listing logic in the
client. Revisit when: a mirror or CDN in front of GitHub is needed for users
who cannot reach it, or a website deployment should own the feed.

The document is a Wails update manifest, schema version 1:

```json
{
  "schemaVersion": 1,
  "version": "0.2.0-beta.1",
  "channel": "beta",
  "name": "dsh-work 0.2.0-beta.1",
  "publishedAt": "2026-10-13T00:00:00Z",
  "artifacts": [
    {
      "url": "https://github.com/dsh-work-dev/dsh-work/releases/download/0.2.0-beta.1/dsh-work-0.2.0-beta.1-windows-amd64-installer.exe",
      "platform": "windows",
      "arch": "amd64",
      "filename": "dsh-work-0.2.0-beta.1-windows-amd64-installer.exe",
      "filetype": "exe",
      "size": 12345678,
      "digestAlgo": "sha512",
      "digest": "base64 sha-512 of the signed installer"
    }
  ]
}
```

Rules the pipeline enforces on that document before it can reach a feed:

- `version` is the identity this run computed and `channel` is the channel the
  feed belongs to. An installation refuses a feed that advertises another
  channel.
- Every artifact carries the `sha512` digest of the signed installer, written by
  `wails3 updater manifest` and re-checked against the uploaded bytes with
  `wails3 updater verify` before publication. The manifest itself is not
  signed; see *Update trust*.
- The single `windows`/`amd64` artifact is the installer, `filename` and
  `filetype` are set explicitly, and `url` is the absolute GitHub Release asset
  URL. The URL is not cosmetic: the update provider resolves the artifact URL
  and the installation reads it back out of the release metadata.
- `schemaVersion` is 1. A newer document is ignored by the client. A change to
  this schema is a client change first.

`wails3 updater manifest` deliberately does not emit `filename`, `filetype` or
any local path, so the pipeline post-processes and re-verifies the manifest
rather than publishing the raw CLI output. The check also refuses a manifest
that contains a runner path.

## Workflows

| File | Role |
| --- | --- |
| `.github/workflows/stable.yml` | stable entry point, tag `vX.Y.Z` |
| `.github/workflows/beta.yml` | beta entry point, tag `vX.Y.Z-beta.N` |
| `.github/workflows/nightly.yml` | nightly entry point: `17 19 * * *` on `main`, dispatch |
| `.github/workflows/release.yml` | the pipeline, `workflow_call` only |
| `.github/workflows/promote-feed.sh` | the feed promotion decision |
| `.github/workflows/promote-feed.test.sh` | local test harness for that decision |
| `.github/workflows/pc.yml` | package checks for pushes and pull requests |

The entry points only choose a trigger and a channel. They call one reusable
workflow, so all three channels run the same code and the same gates, and a
wrong channel cannot publish: the version shape, the channel, and the ref the
run may publish from are all checked against each other.

Nothing in this pipeline runs on a pull request. `pc.yml` keeps running the
frontend and Go suites for pull requests and never receives secrets, the
code-signing certificate, or the right to publish.

`cancel-in-progress` is `false` everywhere. A cancelled run can already hold a
draft release or the promotion lock, so releases queue instead of cancelling
one another; the nightly group keeps at most one pending run.

### Pipeline stages

Every run goes through the same stages in the same order:

1. **plan** (`ubuntu-latest`) — compute the one release identity, then decide
   what this run may publish. The decision is reported in the job summary
   either way.
2. **build** (`windows-latest`) — sync the identity into the build metadata,
   build the GUI and CLI binaries with the release identity linked in, build
   the installer with the pinned NSIS, verify the installer bytes against the
   sidecar, verify the installer's own `ProductVersion`/`FileVersion`, then
   upload the candidate package as a CI artifact. When signing is configured
   the binaries and the installer are signed and all three are checked against
   the publisher.
3. **publish** (`ubuntu-latest`, `contents: write`) — only when the plan allows
   publication. Re-verify the downloaded bytes, inspect the published/draft
   state of the version, create the GitHub Release, re-download the asset and
   compare digests, build the channel manifest, verify its digest against the
   installer, and upload the manifest as a release asset.
4. **promote** (`ubuntu-latest`, `contents: write`) — the single serialized
   section that writes the channel feed.
5. **verify-feed** (`windows-latest`) — fetch the published feed and the
   published installer over HTTPS, check the channel, the digest against the
   bytes, and report the state of the in-place update check.

Signing, when configured, happens inside `scripts/build-windows.ps1 -Sign` (both binaries) and
`scripts/package-windows.ps1 -Sign` (the installer, before its checksum is
written), through `scripts/windows-signing.ps1`. The packager refuses to wrap
binaries that are not signed by the publisher. The NSIS uninstaller inside the
installer is not signed separately.

## Feed promotion

The `promote` job is the only writer of the `updater-<channel>` releases:

- it runs in the repository-wide concurrency group `feed-promotion` with
  `cancel-in-progress: false`, so only one promotion is in flight and a queued
  run waits;
- it creates `updater-<channel>` on the first publication of that channel,
  downloads the live `update.json`, and replaces it with
  `gh release upload --clobber` only when the decision below is `promote`;
- it downloads the asset again and checks it offers the version just
  published. The replacement is a delete followed by an upload, so a client
  that checks in that instant sees a failed check and tries again later.

Inside that job, `promote-feed.sh` decides:

| Live feed | Candidate | Decision |
| --- | --- | --- |
| absent | any validated candidate | promote |
| older version | strictly newer version | promote |
| same version, same artifact digest | — | idempotent success, nothing is re-uploaded |
| strictly newer version | older version | refuse |
| same version, different artifact digest | — | refuse |
| channel that does not match this feed | — | refuse |
| version shape that does not match this channel | — | refuse |
| unreadable, missing version, or missing digest | — | refuse |

It prints `promote` or `idempotent` on stdout, prints the reason for any refusal
on stderr, and exits non-zero on a refusal. An older candidate never overwrites
a newer feed, and a feed that cannot be compared is never replaced blindly.

## Publishing a release

Stable and beta are published by pushing a tag on the commit to release:

```bash
git tag v0.2.0 && git push origin v0.2.0            # stable
git tag v0.2.0-beta.1 && git push origin v0.2.0-beta.1   # beta
```

The tag is the release identity: the version in the tag must belong to the base
in `build/config.yml`, and the ref the run may publish from is checked against
the channel. A tag whose version cannot be resolved to the configured base
fails the run instead of publishing a mislabelled release.

Nightly publishes itself from `main` on a daily schedule at 19:17 UTC and on
manual dispatch; a push or a merge to `main` publishes nothing. The scheduled
run first looks up the newest published nightly release and resolves its tag to
a commit; when that is the current `main`, the run ends without building, so an
unchanged `main` never produces a nightly that offers installations the same
code again. A manual dispatch always builds and publishes. Nightly needs no
per-run approval.

`stable.yml` triggers on `v[0-9]*` but excludes any tag containing `-`, so a
beta tag runs only `beta.yml`. Nightly
retention is a report only: the pipeline never deletes a release, and no
retention job exists yet, so old nightly releases accumulate until a retention
policy is decided.

## Optional signing secrets and variables

No secret is required to publish. These four turn on signing; the certificate
and the publisher must be set together, and a run with only one of them
refuses to publish.

| Name | Kind | Used by |
| --- | --- | --- |
| `WINDOWS_SIGNING_CERTIFICATE` | secret | base64 of the PFX holding the code-signing certificate and its private key |
| `WINDOWS_SIGNING_CERTIFICATE_PASSWORD` | secret | the PFX password |
| `WINDOWS_PUBLISHER` | variable | the certificate's subject CN, compiled into every build and compared with each signature |
| `WINDOWS_SIGNING_TIMESTAMP_URL` | variable, optional | RFC 3161 timestamp server; defaults to `http://timestamp.digicert.com` |

The channel workflows pass the two secrets to `release.yml` explicitly. The PFX
is decoded into the runner's temporary directory, imported into the runner
user's certificate store only while `signtool` runs, so the password never
appears on a command line, and deleted at the end of the build job whatever its
outcome. All channels share one certificate and one publisher.

Configure once, with the CN exactly as the certificate shows it:

```bash
base64 -w0 codesign.pfx | gh secret set WINDOWS_SIGNING_CERTIFICATE
gh secret set WINDOWS_SIGNING_CERTIFICATE_PASSWORD
gh variable set WINDOWS_PUBLISHER --body "<subject CN of the certificate>"
```

To sign locally, set `WINDOWS_SIGNING_CERTIFICATE_FILE` and
`WINDOWS_SIGNING_CERTIFICATE_PASSWORD` and pass `-Publisher` and `-Sign` to both
scripts. The signature must be valid on the signing machine, so a self-signed
test certificate has to be trusted there first.

## Fail-closed behaviour

A run publishes nothing unless every prerequisite is present, and the reason is
reported rather than guessed. Publication is blocked when:

- only one of the code-signing certificate and the publisher name is
  configured;
- `scripts/build-windows.ps1` cannot embed the channel, the feed root and the
  publisher, or cannot sign, because a package built without them can never
  update.

In every one of those cases the run still builds, verifies and uploads the
candidate package as a CI artifact for diagnosis, and stops before any release
or feed is touched.

An already published release is never rewritten. A rerun inspects the version's
published and draft state: a published release fails the run, and only a draft
is deleted and recreated. Re-publishing the same version with different bytes
is refused, not overwritten.

## Current status

Nothing has been published yet:

| Prerequisite | State |
| --- | --- |
| code-signing certificate and publisher | not configured; releases are published unsigned |
| release tags | none exist |
| GitHub Releases | none exist |

Nothing blocks the first publication: the first tag, scheduled nightly or
manual dispatch creates
the versioned release and the `updater-<channel>` release.

Everything upstream of the feed has been verified locally against the real
scripts — the preflight invocations, the version shapes, and the promotion
decision (see *Verification*). The Windows build, the installer packaging, the
GitHub Release creation, the feed promotion and the in-place update check
cannot be exercised on this machine and remain unproven until the pipeline runs
on GitHub.

## Verification

What can be checked without credentials:

```bash
# the version the preflight computes for each channel
node scripts/release-version.mjs --channel nightly --date 2026-10-13 \
  --run-id 9999999999 --source-sha deadbeef --last-stable 0.0.0
node scripts/release-version.mjs --channel beta --version v0.1.0-beta.1 \
  --source-sha deadbeef

# the promotion decision, with the failure cases
bash .github/workflows/promote-feed.test.sh .github/workflows/promote-feed.sh

# every workflow file must parse and pass the linter
actionlint .github/workflows/*.yml
```

`promote-feed.test.sh` runs the real decision script against synthetic
manifests. It covers the missing feed, the strictly newer candidate, the
idempotent republication, the same version with different bytes, the older
candidate, the wrong channel, a feed holding another channel's version shape, a
malformed feed, a feed with no digest, and version ordering for beta and nightly.

Run the harness from the repository root. It keeps its scratch files in `.task/`
because this host can run a Windows `node.exe` under bash, and a Windows node
cannot read a path under `/tmp`.

## Publishing a feed by hand

If a run fails after the release was created but before the feed was promoted,
the feed can be repaired by hand, with the same rules:

1. download the published `dsh-work-<version>-windows-amd64-manifest.json`
   release asset;
2. `wails3 updater verify -manifest <manifest> -dir <dir>` with the installer
   next to it, and, for a signed release, check the installer's signer is the
   publisher
   (`. scripts/windows-signing.ps1; Assert-AuthenticodePublisher -Path <installer> -Publisher <publisher>`);
3. download the live feed with
   `gh release download updater-<channel> --pattern update.json`, run
   `bash .github/workflows/promote-feed.sh <manifest> <live update.json> <version> <channel> <installer>`
   and act on its decision: `promote` replaces the feed, `idempotent` changes
   nothing, and a non-zero exit with a reason on stderr means the feed must not
   be touched by hand either;
4. copy the manifest to `update.json` and
   `gh release upload updater-<channel> update.json --clobber`.

A hand promotion must never publish a feed document that `wails3 updater verify`
rejects.

## Limitations

- macOS and Linux release legs are out of scope. Only the Windows installer is
  published, and only `amd64` is built.
- The N-1 → N in-place update is reported, not performed: the pipeline proves
  the feed and the published bytes, and a machine that already
  runs the previous release performs the actual update.
- Nightly retention is not implemented; nothing deletes old nightlies.
- The feed is served only from GitHub; there is no mirror for users who cannot
  reach `github.com`.
