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
| nightly | `X.Y.Z-nightly.YYYYMMDD.<run id>` | `main`, or the default branch | push to `main`, manual dispatch, daily schedule |

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
| `DistributionPublicKey` | the base64 Ed25519 public key the build trusts |

`scripts/build-windows.ps1` refuses a release build that does not pin a channel
and a valid 32-byte Ed25519 public key, and verifies after linking that the
binary really carries the identity (the linker silently ignores `-X` for a
symbol it cannot find). A local build passes no identity at all and stays
non-updatable on purpose.

## Feeds

Every channel publishes exactly one JSON document at the feed root:

```
https://dsh-work-dev.github.io/dsh-work/updates/stable.json
https://dsh-work-dev.github.io/dsh-work/updates/beta.json
https://dsh-work-dev.github.io/dsh-work/updates/nightly.json
```

The URL is not a convention that can drift: `internal/desktopapp/update.go`
builds `<feed root>/<channel>.json`, and a build with no feed root logs that
updates stay unavailable rather than falling back to a default.

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
      "digest": "base64 sha-512 of the installer",
      "signatureAlgo": "ed25519ph",
      "signature": "base64 ed25519ph signature over that digest"
    }
  ]
}
```

Rules the pipeline enforces on that document before it can reach a feed:

- `version` is the identity this run computed and `channel` is the channel the
  feed belongs to. An installation refuses a feed that advertises another
  channel.
- Every artifact carries a `sha512` digest and an `ed25519ph` signature over
  that digest. The signature is produced by `wails3 updater manifest -key` and
  is re-verified against the compiled-in public key before publication, so an
  unsigned or wrong-key manifest stops the run.
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
| `.github/workflows/nightly.yml` | nightly entry point: `main`, dispatch, `17 19 * * *` |
| `.github/workflows/release.yml` | the pipeline, `workflow_call` only |
| `.github/workflows/promote-feed.sh` | the feed promotion decision |
| `.github/workflows/promote-feed.test.sh` | local test harness for that decision |
| `.github/workflows/feed-index.html` | the page published next to the feeds |
| `.github/workflows/pc.yml` | package checks for pushes and pull requests |

The entry points only choose a trigger and a channel. They call one reusable
workflow, so all three channels run the same code and the same gates, and a
wrong channel cannot publish: the version shape, the channel, and the ref the
run may publish from are all checked against each other.

Nothing in this pipeline runs on a pull request. `pc.yml` keeps running the
frontend and Go suites for pull requests and never receives secrets, a signing
key, or the right to publish.

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
   upload the candidate package as a CI artifact.
3. **publish** (`ubuntu-latest`, `contents: write`) — only when the plan allows
   publication. Re-verify the downloaded bytes, inspect the published/draft
   state of the version, create the GitHub Release, re-download the asset and
   compare digests, build and sign the channel manifest with the Ed25519 key,
   verify it against the public key, and upload the manifest as a release
   asset.
4. **promote** (`ubuntu-latest`, `contents: write`) — the single serialized
   section that writes the channel feed.
5. **verify-feed** (`windows-latest`) — fetch the published feed and the
   published installer over HTTPS, check the channel, the digest against the
   bytes, and report the state of the in-place update check.

The OS signing stage between build and package is where an Authenticode
certificate would sign the binaries and the installer. No certificate exists
for this project today, so no OS signing step is present; see *Blocked*.

## Feed promotion

The three channels share one Pages site, so there is exactly one writer and one
lock. The lock is the `gh-pages` branch ref, and the write is a push:

- the `promote` job runs in the repository-wide concurrency group
  `feed-promotion` with `cancel-in-progress: false`, so only one promotion is
  in flight and a queued run waits;
- the job re-reads `gh-pages` at the start of every attempt, merges the
  candidate document for its own channel into the feeds that are published, and
  republishes the whole site;
- the push is the compare-and-swap. A push that loses the race is rejected
  because the branch moved; the run re-reads the branch and retries, at most
  five times.

Inside the lock, `promote-feed.sh` decides:

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

Nightly publishes itself from `main` on every push, on a daily schedule at
19:17 UTC, and on manual dispatch. It needs no per-run approval. Nightly
retention is a report only: the pipeline never deletes a release, and no
retention job exists yet, so old nightly releases accumulate until a retention
policy is decided.

## Required secrets and variables

The pipeline reads one key pair. It is configured per channel family, so a
nightly can never sign with the stable key:

| Name | Kind | Used by |
| --- | --- | --- |
| `RELEASE_UPDATER_KEY` | secret | stable and beta signing |
| `RELEASE_UPDATER_PUBLIC_KEY` | secret or variable | stable and beta verification and build identity |
| `NIGHTLY_UPDATER_KEY` | secret | nightly signing |
| `NIGHTLY_UPDATER_PUBLIC_KEY` | secret or variable | nightly verification and build identity |

The public key may be a `.pub` PEM file, or the base64 (padded or not) or hex
text of the 32 raw key bytes. It is passed to the build as a file, never as a
command line argument, so key material never appears in a process list. The
private key is written to a runner-temporary file with mode `600`, used for one
`wails3 updater manifest` call, and deleted in the same step.

Create the pair once:

```bash
wails3 updater genkey -out release-updater.key
gh secret set RELEASE_UPDATER_KEY < release-updater.key
gh secret set RELEASE_UPDATER_PUBLIC_KEY < release-updater.key.pub
```

Losing the private key means the published builds keep trusting a key nobody
can sign with. There is no rotation procedure yet; rotating the key requires a
release that carries the new public key.

## Fail-closed behaviour

A run publishes nothing unless every prerequisite is present, and the reason is
reported rather than guessed. Publication is blocked when:

- the updater private key or the public key is not configured for the channel;
- `scripts/build-windows.ps1` cannot embed the channel, the feed root and the
  public key, because a package built without them can never update;
- GitHub Pages is not enabled for the repository, because a feed that Pages
  does not serve is a release that updates nobody.

In every one of those cases the run still builds, verifies and uploads the
candidate package as a CI artifact for diagnosis, and stops before any release
or feed is touched. An unsigned or unverifiable package is never promoted to a
feed.

An already published release is never rewritten. A rerun inspects the version's
published and draft state: a published release fails the run, and only a draft
is deleted and recreated. Re-publishing the same version with different bytes
is refused, not overwritten.

## Current status

Publication is blocked today, for reasons that are facts about the repository
rather than bugs in the pipeline:

| Prerequisite | State |
| --- | --- |
| GitHub Pages site | **not enabled** (`has_pages: false`, no `gh-pages` branch) |
| updater key pair | **not configured** (`gh secret list` and `gh variable list` are empty) |
| release tags | none exist |
| GitHub Releases | none exist |
| Authenticode certificate | none; no OS signing step exists |

The pipeline's behaviour in this state is the intended one: it builds and keeps
CI artifacts, and it refuses to publish or promote a feed.

Everything upstream of the feed has been verified locally against the real
scripts — the preflight invocations, the version shapes, and the promotion
decision (see *Verification*). The Windows build, the installer packaging, the
GitHub Release creation, the Pages publication and the in-place update check
cannot be exercised on this machine and remain unproven until they run on a
runner with the secrets and a Pages site.

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
2. `wails3 updater verify -manifest <manifest> -publickey <public key> -dir <dir>`
   with the installer next to it;
3. run `bash .github/workflows/promote-feed.sh <manifest> <current feed> <version> <channel> <installer>`
   and act on its decision: `promote` replaces the feed, `idempotent` changes
   nothing, and a non-zero exit with a reason on stderr means the feed must not
   be touched by hand either;
4. commit the result to `gh-pages` and push.

A hand promotion must never publish a feed document that `wails3 updater verify`
rejects.

## Limitations

- macOS and Linux release legs are out of scope. Only the Windows installer is
  published, and only `amd64` is built.
- The N-1 → N in-place update is reported, not performed: the pipeline proves
  the feed, the signature and the published bytes, and a machine that already
  runs the previous release performs the actual update.
- Nightly retention is not implemented; nothing deletes old nightlies.
- The feed site is served by GitHub Pages from the `gh-pages` branch; the
  pipeline does not use the Pages deployment API or an environment.
