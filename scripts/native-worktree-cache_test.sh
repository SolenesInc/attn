#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-native-cache-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT
GOOS="$(go env GOHOSTOS)"
GOARCH="$(go env GOHOSTARCH)"
export GOOS GOARCH CGO_ENABLED=1
export GOFLAGS=-trimpath CC=cc
platform="${GOOS}_${GOARCH}"
case "$platform" in
  darwin_arm64|linux_amd64|linux_arm64) ;;
  *) echo "native cache test skipped on $platform"; exit 0 ;;
esac
export XDG_CACHE_HOME="$work/cache with spaces"
export NATIVE_TEST_ASSET="$work/asset.tar.gz" NATIVE_TEST_DOWNLOADS="$work/downloads"
mkdir -p "$work/bin" "$work/repo/scripts/lib" "$work/repo/internal/ghosttyvt" "$work/repo/cmd/probe"
cp "$root/scripts/build-libghostty-vt.sh" "$work/repo/scripts/"
cp "$root/scripts/lib/libghostty-vt.sh" "$root/scripts/lib/release-asset.sh" "$work/repo/scripts/lib/"
cp "$root/Makefile" "$root/.tool-versions" "$work/repo/"
cat > "$work/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == auth ]]; then exit 0; fi
while [[ "$#" -gt 0 ]]; do
  if [[ "$1" == --output ]]; then
    cp "$NATIVE_TEST_ASSET" "$2"
    echo download >> "$NATIVE_TEST_DOWNLOADS"
    exit 0
  fi
  shift
done
exit 1
GH
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH"

build_asset() {
  local value="$1" artifact="$work/artifact/ghostty-vt/$platform"
  mkdir -p "$artifact/include/ghostty" "$artifact/lib"
  printf 'const char *cache_value(void);\n' > "$artifact/include/ghostty/vt.h"
  printf 'const char *cache_value(void) { return "%s"; }\n' "$value" > "$work/value.c"
  cc -c "$work/value.c" -o "$work/value.o"
  ar rcs "$artifact/lib/libghostty-vt.a" "$work/value.o"
  printf 'fixture license\n' > "$artifact/LICENSE"
  tar -czf "$NATIVE_TEST_ASSET" -C "$work/artifact" ghostty-vt
}

write_lock() {
  local repo="$1" key sha
  key="$(printf 'fixture\n' | shasum -a 256 | awk '{ print $1 }')"
  sha="$(shasum -a 256 "$NATIVE_TEST_ASSET" | awk '{ print $1 }')"
  printf 'key=%s\nsha256_%s=%s\n' "$key" "$platform" "$sha" > "$repo/ghostty-vt-native.lock"
}

printf 'fixture\n' > "$work/repo/ghostty-vt.pin"
printf 'module cacheprobe\n' > "$work/repo/go.mod"
printf '/third_party/\n/internal/ghosttyvt/native_paths_generated_*.go\n' > "$work/repo/.gitignore"
cat > "$work/repo/internal/ghosttyvt/native.go" <<'GO'
package ghosttyvt

/*
#include <ghostty/vt.h>
*/
import "C"

func Value() string { return C.GoString(C.cache_value()) }
GO
cat > "$work/repo/cmd/probe/main.go" <<'GO'
package main

import (
    "fmt"
    "cacheprobe/internal/ghosttyvt"
)

func main() { fmt.Println(ghosttyvt.Value()) }
GO
build_asset old
write_lock "$work/repo"
git -C "$work/repo" init -q
git -C "$work/repo" add .
git -C "$work/repo" -c user.name=test -c user.email=test@example.invalid -c commit.gpgsign=false commit -qm fixture
git -C "$work/repo" worktree add -q --detach "$work/second" HEAD

(cd "$work/repo" && ./scripts/build-libghostty-vt.sh && go build -x ./internal/ghosttyvt) > "$work/first.log" 2>&1 || { cat "$work/first.log"; exit 1; }
(cd "$work/second" && ./scripts/build-libghostty-vt.sh && go build -x ./internal/ghosttyvt) > "$work/second.log" 2>&1 || { cat "$work/second.log"; exit 1; }
grep -q '/compile .* -p cacheprobe/internal/ghosttyvt ' "$work/first.log" || { cat "$work/first.log"; exit 1; }
if grep -q '/compile .* -p cacheprobe/internal/ghosttyvt ' "$work/second.log"; then
  cat "$work/second.log"
  echo 'the second worktree recompiled the unchanged native package' >&2
  exit 1
fi
[[ "$(wc -l < "$NATIVE_TEST_DOWNLOADS" | tr -d ' ')" == 1 ]]
[[ "$(cd "$work/second" && go run ./cmd/probe)" == old ]]

rm "$work/second/internal/ghosttyvt/native_paths_generated_$platform.go"
make -s -C "$work/second" "third_party/ghostty-vt/$platform/lib/libghostty-vt.a"
[[ "$(cd "$work/second" && go run ./cmd/probe)" == old ]]
[[ "$(wc -l < "$NATIVE_TEST_DOWNLOADS" | tr -d ' ')" == 1 ]]

cp -R "$XDG_CACHE_HOME" "$work/restored cache"
XDG_CACHE_HOME="$work/restored cache" bash "$work/second/scripts/build-libghostty-vt.sh"
[[ "$(cd "$work/second" && go run ./cmd/probe)" == old ]]
[[ "$(wc -l < "$NATIVE_TEST_DOWNLOADS" | tr -d ' ')" == 1 ]]

build_asset new
write_lock "$work/second"
(cd "$work/second" && ./scripts/build-libghostty-vt.sh)
[[ "$(cd "$work/second" && go run ./cmd/probe)" == new ]]
[[ "$(cd "$work/repo" && go run ./cmd/probe)" == old ]]
[[ "$(wc -l < "$NATIVE_TEST_DOWNLOADS" | tr -d ' ')" == 2 ]]
echo 'native worktree cache: OK'
