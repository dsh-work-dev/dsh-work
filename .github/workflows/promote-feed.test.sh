#!/usr/bin/env bash
# Local harness for .github/workflows/promote-feed.sh.
#
# It runs the real script against synthetic manifests and artifacts, with shims
# for the two tools the GitHub runner image has and this host does not (jq and a
# `node` reachable from bash). Every case below asserts the decision the feed
# contract requires.
set -uo pipefail

script="${1:?usage: promote-feed.test.sh <path to promote-feed.sh>}"
# Scratch space lives inside the working tree (and .task/ is already ignored):
# this host may run a Windows node binary under bash, and a Windows node cannot
# read the /tmp path that bash would otherwise hand it. Run from the repo root.
mkdir -p .task/promote-feed-test
work="$(mktemp -d .task/promote-feed-test/run.XXXXXX)"
trap 'rm -rf "$work"' EXIT
shims="$work/shims"
mkdir -p "$shims"

# The first node on PATH here is a shim that swallows `-e`, so the harness
# probes for a real one and passes it to the script through PROMOTE_FEED_NODE.
# On the runner image the script just uses `node`, and this whole block is a
# no-op. Set PROMOTE_FEED_NODE explicitly to skip the search.
node_bin="${PROMOTE_FEED_NODE:-}"
if [ -z "$node_bin" ] && node -e 'process.exit(0)' > /dev/null 2>&1; then
  node_bin="$(command -v node)"
fi
if [ -z "$node_bin" ]; then
  for candidate in $(compgen -G '/mnt/c/Users/*/AppData/Local/fnm_multishells/*/node.exe' 2>/dev/null | head -5); do
    if "$candidate" -e 'process.exit(0)' > /dev/null 2>&1; then
      node_bin="$candidate"
      break
    fi
  done
fi
if [ -z "$node_bin" ]; then
  echo "promote-feed.test.sh: no node that supports -e; set PROMOTE_FEED_NODE" >&2
  exit 1
fi
export PROMOTE_FEED_NODE="$node_bin"

# This host has no jq. The runner image does, so the shim implements exactly the
# four queries promote-feed.sh issues against a live feed and nothing else; it
# is a local test aid, not a dependency of the workflow.
cat > "$shims/jq" <<'SHIM'
#!/usr/bin/env bash
query=""
field=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -r) ;;
    -e) ;;
    -*) ;;
    *)
      if [ -z "$query" ]; then query="$1"; else field="$1"; fi
      ;;
  esac
  shift
done
# Same reason as PROMOTE_FEED_NODE above: the node first on PATH here is a shim.
"${PROMOTE_FEED_NODE:-node}" -e '
  const fs = require("fs");
  const [query, file] = process.argv.slice(1);
  const doc = JSON.parse(fs.readFileSync(file, "utf8"));
  const answer = () => {
    if (query.includes(".version")) return doc.version ?? "";
    if (query.includes(".channel")) return doc.channel ?? "";
    if (query.includes("digest")) {
      const match = (doc.artifacts ?? []).find(
        (a) => a.platform === "windows" && a.arch === "amd64"
      );
      return match?.digest ?? "";
    }
    console.error("jq shim: unsupported query " + query);
    process.exit(3);
  };
  console.log(answer());
' -- "$query" "$field"
SHIM
chmod +x "$shims/jq"
export PATH="$shims:$PATH"

pass=0
fail=0

# A real SHA-512, computed with the Git for Windows coreutils.
digest_of() {
  sha512sum "$1" | cut -d' ' -f1
}

# base64 of the raw digest, the form the Wails manifest carries.
b64_of() {
  openssl dgst -sha512 -binary "$1" | base64 | tr -d '\n'
}

artifact="$work/installer.exe"
printf 'dsh-work installer payload v1' > "$artifact"
artifact2="$work/installer2.exe"
printf 'dsh-work installer payload v2' > "$artifact2"

manifest() { # version channel artifact -> path (stdout)
  local version="$1" channel="$2" file="$3" out="$4"
  local digest
  digest=$(b64_of "$file")
  cat > "$out" <<JSON
{
  "schemaVersion": 1,
  "version": "$version",
  "channel": "$channel",
  "name": "dsh-work $version",
  "publishedAt": "2026-10-13T00:00:00Z",
  "artifacts": [
    {
      "url": "https://example.invalid/$version/$(basename "$file")",
      "platform": "windows",
      "arch": "amd64",
      "filename": "$(basename "$file")",
      "filetype": "exe",
      "size": $(wc -c < "$file" | tr -d ' '),
      "digestAlgo": "sha512",
      "digest": "$digest"
    }
  ]
}
JSON
}

expect() { # description expected-decision candidate live version channel artifact
  local description="$1" want="$2"
  shift 2
  local got status
  got=$(bash "$script" "$@" 2>"$work/stderr")
  status=$?
  if [ "$status" -eq 0 ] && [ "$got" = "$want" ]; then
    pass=$((pass + 1))
    printf 'ok   %s -> %s\n' "$description" "$got"
  elif [ "$status" -ne 0 ] && [ "$want" = "REFUSED" ]; then
    pass=$((pass + 1))
    printf 'ok   %s -> refused (%s)\n' "$description" "$(tail -n 1 "$work/stderr")"
  else
    fail=$((fail + 1))
    printf 'FAIL %s -> got exit %s stdout %q stderr %q, wanted %s\n' \
      "$description" "$status" "$got" "$(tail -n 1 "$work/stderr")" "$want"
  fi
}

stable_new="$work/stable-new.json"
stable_old="$work/stable-old.json"
manifest 0.2.0 stable "$artifact" "$stable_new"
manifest 0.1.0 stable "$artifact" "$stable_old"

# 1. no live feed yet -> promote
expect "first publication of a channel" promote "$stable_new" "$work/updates/stable.json" 0.2.0 stable "$artifact"

# 2. strictly newer -> promote
expect "strictly newer candidate" promote "$stable_new" "$stable_old" 0.2.0 stable "$artifact"

# 3. same version, same bytes -> idempotent
expect "same version and same bytes" idempotent "$stable_new" "$stable_new" 0.2.0 stable "$artifact"

# 4. same version, different bytes -> refuse
expect "same version, different bytes" REFUSED "$stable_new" "$stable_new" 0.2.0 stable "$artifact2"

# 5. older candidate -> refuse, never overwrite a newer feed
expect "older candidate" REFUSED "$stable_old" "$stable_new" 0.1.0 stable "$artifact"

# 6. candidate that is not the manifest of this run -> refuse
expect "candidate version does not match the run" REFUSED "$stable_old" "$work/updates/stable.json" 0.2.0 stable "$artifact"

# 7. wrong channel in the candidate -> refuse
manifest 0.2.0 nightly "$artifact" "$work/nightly-as-stable.json"
expect "candidate channel does not match the feed" REFUSED "$work/nightly-as-stable.json" "$work/updates/stable.json" 0.2.0 stable "$artifact"

# 8. live feed for another channel -> refuse
expect "live feed belongs to another channel" REFUSED "$stable_new" "$work/nightly-as-stable.json" 0.2.0 stable "$artifact"

# 9. missing artifact -> refuse
expect "artifact bytes are absent" REFUSED "$stable_new" "$stable_old" 0.2.0 stable "$work/absent.exe"

# 10. nightly rollover, same base
manifest 0.1.0-nightly.20261012.111 nightly "$artifact" "$work/n1.json"
manifest 0.1.0-nightly.20261013.222 nightly "$artifact" "$work/n2.json"
expect "newer nightly date" promote "$work/n2.json" "$work/n1.json" 0.1.0-nightly.20261013.222 nightly "$artifact"
expect "older nightly date" REFUSED "$work/n1.json" "$work/n2.json" 0.1.0-nightly.20261012.111 nightly "$artifact"

# 11. nightly run ids within one day
manifest 0.1.0-nightly.20261013.999 nightly "$artifact" "$work/n3.json"
expect "higher nightly run id" promote "$work/n3.json" "$work/n2.json" 0.1.0-nightly.20261013.999 nightly "$artifact"

# 12. beta sequence
manifest 0.1.0-beta.2 beta "$artifact" "$work/b2.json"
manifest 0.1.0-beta.10 beta "$artifact" "$work/b10.json"
expect "beta.10 is newer than beta.2" promote "$work/b10.json" "$work/b2.json" 0.1.0-beta.10 beta "$artifact"
expect "beta.2 is older than beta.10" REFUSED "$work/b2.json" "$work/b10.json" 0.1.0-beta.2 beta "$artifact"

# 13. a malformed live feed is a refusal, never a blind overwrite
printf 'not json' > "$work/broken.json"
expect "live feed is not JSON" REFUSED "$stable_new" "$work/broken.json" 0.2.0 stable "$artifact"

# 14. live feed without a digest at the same version
jq_missing="$(printf '{"schemaVersion":1,"version":"0.2.0","channel":"stable","artifacts":[{"platform":"windows","arch":"amd64","url":"https://example.invalid/a.exe"}]}')"
printf '%s' "$jq_missing" > "$work/nodigest.json"
expect "same version on the live feed without a digest" REFUSED "$stable_new" "$work/nodigest.json" 0.2.0 stable "$artifact"

# 15. a feed may only hold the version shape of its own channel: a nightly
#     candidate must never be compared against, or replace, a release that sits
#     on the nightly feed, and a release must never land on a prerelease feed.
manifest 0.1.0-beta.3 beta "$artifact" "$work/b3.json"
manifest 0.1.0-nightly.20261013.555 nightly "$artifact" "$work/n4.json"
manifest 0.1.0 stable "$artifact" "$work/s100.json"
manifest 0.1.0 beta "$artifact" "$work/b100.json"
expect "release supersedes beta of the same base" promote "$work/b100.json" "$work/b3.json" 0.1.0 beta "$artifact"
expect "nightly does not replace the release of the same base" REFUSED "$work/n4.json" "$work/s100.json" 0.1.0-nightly.20261013.555 nightly "$artifact"
expect "a stable feed holding a beta is refused" REFUSED "$work/s100.json" "$work/b100.json" 0.1.0 stable "$artifact"
expect "a nightly feed holding a stable release is refused" REFUSED "$work/n4.json" "$work/s100.json" 0.1.0-nightly.20261013.555 nightly "$artifact"

# 15b. the version shape of the live feed is checked even when the channel in
#      the document agrees with the file it is read from.
printf '%s' "$(printf '{"schemaVersion":1,"version":"0.1.0","channel":"nightly","artifacts":[{"platform":"windows","arch":"amd64","digest":"ZGln"}]}')" > "$work/nightly-stable-shape.json"
expect "a nightly feed holding a release shape is refused" REFUSED \
  "$work/n4.json" "$work/nightly-stable-shape.json" 0.1.0-nightly.20261013.555 nightly "$artifact"

# 16. a live feed whose channel does not match the file it is read from is a
#     refusal, because a promotion would otherwise write one channel's release
#     onto another channel's feed.
expect "live feed for the wrong channel is refused" REFUSED \
  "$work/s100.json" "$work/n2.json" 0.1.0 stable "$artifact"

printf '\n%s passed, %s failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
