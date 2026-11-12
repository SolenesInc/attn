#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-native-build-clean-test.XXXXXX")"
holder=""
release_lock() {
  [ -n "$holder" ] || return 0
  echo release >"$work/release"
  wait "$holder" || true
  holder=""
}
trap 'release_lock; rm -rf "$work"' EXIT
export ATTN_NATIVE_BUILD_ROOT="$work/build root"
checkouts="$work/check outs"

build() {
  local checkout="$checkouts/$1" mib="$2" dir
  mkdir -p "$checkout/native"
  : >"$checkout/native/Cargo.toml"
  dir="$ATTN_NATIVE_BUILD_ROOT$checkout/native"
  mkdir -p "$dir/debug/deps" "$dir/debug/.fingerprint"
  : >"$dir/CACHEDIR.TAG"
  : >"$dir/debug/.cargo-build-lock"
  head -c $((mib * 1048576)) /dev/zero >"$dir/debug/deps/libexample.rlib"
}

age() {
  local days="$1" dir="$ATTN_NATIVE_BUILD_ROOT$checkouts/$2/native"
  find "$dir" -print0 | xargs -0 perl -e 'my $t = time - shift() * 86400; utime $t, $t, @ARGV' "$days"
}

hold_lock() {
  mkfifo "$work/locked" "$work/release"
  perl -MFcntl=:flock -e '
    open(my $lock, "<", shift) or die;
    flock($lock, LOCK_EX) or die;
    open(my $out, ">", shift) or die; print $out "locked\n"; close $out;
    open(my $in, "<", shift) or die; my $line = <$in>;
  ' "$ATTN_NATIVE_BUILD_ROOT$checkouts/$1/native/debug/.cargo-build-lock" "$work/locked" "$work/release" &
  holder=$!
  read -r _ <"$work/locked"
}

present() { [ -d "$ATTN_NATIVE_BUILD_ROOT$checkouts/$1/native" ]; }
fail() { echo "native-build-clean: $*" >&2; cat "$work/out" >&2; exit 1; }

build deleted 1
build stale 1
build building 1
build fresh 1
rm -r "$checkouts/deleted"
age 10 stale
age 10 building
hold_lock building

"$root/scripts/native-build-clean" >"$work/out"
present deleted && fail "kept the output of a checkout that no longer exists"
present stale && fail "kept a build unused for 10 days"
present building || fail "removed a build whose lock a running build holds"
present fresh || fail "removed a fresh build"
grep -qF "its checkout $checkouts/deleted/native no longer exists" "$work/out" || fail "did not say why it removed the deleted checkout's output"
grep -qF "not built for 10 days, past 3" "$work/out" || fail "did not say why it removed the stale build"
grep -qF "kept $ATTN_NATIVE_BUILD_ROOT$checkouts/building/native" "$work/out" || fail "did not name the build it kept because it is in use"
grep -qF "budget 64.0 GiB" "$work/out" || fail "did not print the budget"
release_lock

"$root/scripts/native-build-clean" >"$work/out"
[ ! -s "$work/out" ] || fail "printed when it removed nothing and was under budget"

build older 2
age 1 older
build newer 2
ATTN_NATIVE_BUILD_BUDGET_MIB=4 "$root/scripts/native-build-clean" >"$work/out"
grep -qF "is over its budget of 0.0 GiB" "$work/out" || fail "did not say the output was over budget"
present older && fail "kept the least recently built output while over budget"
present newer || fail "removed more than the budget needed"
grep -qF "least recently built while over budget" "$work/out" || fail "did not say why it removed the older build"

echo 'native build clean: OK'
