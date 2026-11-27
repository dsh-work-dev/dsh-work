#!/usr/bin/env bash
set -euo pipefail

# Build only the formal private broker. The caller supplies the pinned SDK and
# an isolated build directory; this does not download/install or alter sandbox.
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 || $# != 2 ]]; then
  echo 'Usage (Linux x86_64): build-embedded-cef-linux.sh CEF_ROOT BUILD_DIR' >&2
  exit 2
fi
sdk=$(realpath "$1")
mkdir -p "$2"
build=$(realpath "$2")
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cmake -S "$repo/native/embeddedbrowser" -B "$build" \
  -DCEF_ROOT="$sdk" -DCMAKE_BUILD_TYPE=Release -DUSE_SANDBOX=ON
cmake --build "$build" --config Release --target dsh_cef_broker --parallel 3
test -x "$build/Release/dsh_cef_broker"
test -f "$build/Release/chrome-sandbox"
printf 'Formal broker: %s\n' "$build/Release/dsh_cef_broker"
