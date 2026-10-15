#!/usr/bin/env bash
#
# Feed promotion decision: may this candidate replace the feed that is live?
#
# The three channel feeds are one Pages site, so a promotion is the only write
# to that site and it must be safe even when two runs race for it. This script
# only decides; the caller re-reads the branch, applies the decision and pushes.
#
#   promote-feed.sh <candidate-manifest> <live-feed> <version> <channel> <artifact>
#
# Recognised on stdout:
#   promote      the candidate is strictly newer and the live feed should be replaced
#   idempotent   the live feed already offers this exact release and these exact bytes
#
# Anything else is a refusal: the script prints why on stderr and exits non-zero.
#
# The decision is deliberately narrow. A candidate is accepted only if it is
# strictly newer than the live feed, or if it is the same version with the same
# bytes (a retry). An older candidate is never allowed to replace a newer feed,
# and a live feed that cannot be compared safely is a refusal. A channel with no
# live feed yet is a first publication, so that is a promotion.

set -uo pipefail

# The runner image provides jq and node on PATH. The override exists so the
# local harness can point the comparison at a node binary of its choosing.
node_bin="${PROMOTE_FEED_NODE:-node}"

candidate="${1:-}"
live="${2:-}"
version="${3:-}"
channel="${4:-}"
artifact="${5:-}"

usage() {
  echo "usage: promote-feed.sh <candidate-manifest> <live-feed> <version> <channel> <artifact>" >&2
}

refuse() {
  echo "promote-feed: $*" >&2
  exit 1
}

for value in candidate live version channel artifact; do
  if [ -z "${!value}" ]; then
    usage
    refuse "missing argument: $value"
  fi
done

command -v jq > /dev/null 2>&1 || refuse "jq is required to read a feed manifest"
command -v sha512sum > /dev/null 2>&1 || refuse "sha512sum is required to compare artifact bytes"

[ -f "$candidate" ] || refuse "candidate manifest $candidate does not exist"
[ -f "$artifact" ] || refuse "candidate artifact $artifact does not exist"

# The candidate must be the manifest of the release this run built and signed.
candidate_version=$(jq -r '.version // ""' "$candidate")
candidate_channel=$(jq -r '.channel // ""' "$candidate")
[ "$candidate_version" = "$version" ] || refuse "the candidate manifest offers '$candidate_version', this run publishes '$version'"
[ "$candidate_channel" = "$channel" ] || refuse "the candidate manifest is for channel '$candidate_channel', this feed is '$channel'"

# Precedence is SemVer's, applied to the version shapes this product uses:
# X.Y.Z, X.Y.Z-beta.N and X.Y.Z-nightly.YYYYMMDD.RUN. Node is already an input
# of this pipeline, and hand-rolled comparison of the identifiers is the part
# most likely to be wrong, so the comparison lives in a program rather than in
# shell arithmetic.
newer() {
  "$node_bin" -e '
    const parse = (value) => {
      const [core, ...pre] = String(value).split("-");
      const [major, minor, patch] = core.split(".").map((part) => Number.parseInt(part, 10));
      const version = { major, minor, patch, prerelease: pre.join("-") };
      if (version.prerelease.startsWith("beta.")) {
        version.beta = Number.parseInt(version.prerelease.slice("beta.".length), 10);
      }
      if (version.prerelease.startsWith("nightly.")) {
        const parts = version.prerelease.split(".");
        version.nightlyDate = Number.parseInt(parts[1], 10);
        version.nightlyRun = Number.parseInt(parts[2], 10);
      }
      return version;
    };
    const newer = (a, b) => {
      for (const key of ["major", "minor", "patch"]) {
        if (a[key] !== b[key]) return a[key] > b[key];
      }
      // A release outranks any prerelease of the same base; otherwise the
      // stage decides, then the ordinal inside the stage.
      if (a.prerelease === "" || b.prerelease === "") {
        return a.prerelease === "" && b.prerelease !== "";
      }
      if (a.beta !== undefined || b.beta !== undefined) return a.beta > b.beta;
      if (a.nightlyDate !== b.nightlyDate) return a.nightlyDate > b.nightlyDate;
      return a.nightlyRun > b.nightlyRun;
    };
    process.exit(newer(parse(process.argv[1]), parse(process.argv[2])) ? 0 : 1);
  ' -- "$1" "$2"
}

artifact_digest=$(sha512sum "$artifact" | cut -d' ' -f1)

if [ ! -f "$live" ]; then
  echo "promote"
  exit 0
fi

live_version=$(jq -r '.version // ""' "$live" 2>/dev/null) || refuse "the live feed $live is not readable JSON"
live_channel=$(jq -r '.channel // ""' "$live" 2>/dev/null) || refuse "the live feed $live is not readable JSON"
[ -n "$live_version" ] || refuse "the live feed $live carries no version"
[ "$live_channel" = "$channel" ] || refuse "the live feed $live belongs to channel '$live_channel', this feed is '$channel'"

# A feed may only hold the version shape of its own channel. The version shape
# also decides precedence inside a channel, so a feed holding another shape is a
# corrupt feed, not a comparison to make.
case "$live_version" in
  *-nightly.*)
    [ "$channel" = "nightly" ] || refuse "the live $channel feed $live holds prerelease $live_version"
    ;;
  *-beta.*)
    [ "$channel" = "beta" ] || refuse "the live $channel feed $live holds prerelease $live_version"
    ;;
  *)
    [ "$channel" = "stable" ] || refuse "the live $channel feed $live holds release $live_version"
    ;;
esac

if [ "$live_version" = "$version" ]; then
  # The same version twice is allowed only when it means the same release. The
  # live feed describes one windows/amd64 installer; these bytes must be the
  # bytes it already points at.
  live_digest=$(jq -r '[.artifacts[]? | select(.platform == "windows" and .arch == "amd64")][0].digest // ""' "$live")
  [ -n "$live_digest" ] || refuse "the live feed $live offers $version but carries no windows/amd64 digest"
  live_digest=$(printf '%s' "$live_digest" | base64 -d 2>/dev/null | od -An -v -tx1 | tr -d ' \n')
  [ -n "$live_digest" ] || refuse "the live feed $live carries a digest that is not base64"
  if [ "$live_digest" = "$artifact_digest" ]; then
    echo "idempotent"
    exit 0
  fi
  refuse "version $version is already published on $channel with different bytes (live digest $live_digest, candidate digest $artifact_digest)"
fi

if newer "$version" "$live_version"; then
  echo "promote"
  exit 0
fi

refuse "version $version is not newer than the live $channel feed version $live_version"
