#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-native-build-clean-test.XXXXXX")" && pwd)"
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
mkdir "$work/bin"
cat >"$work/bin/df" <<'DF'
#!/usr/bin/env bash
build_kib="$(du -sk "$ATTN_NATIVE_BUILD_ROOT" | cut -f1)"
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
echo "fake $((disk_mib * 1024)) $((other_mib * 1024 + build_kib)) $(((disk_mib - other_mib) * 1024 - build_kib)) 50% /"
DF
chmod +x "$work/bin/df"
export PATH="$work/bin:$PATH" disk_mib=1048576 other_mib=0

build() {
  local checkout="$checkouts/$1" mib="$2" dir
  mkdir -p "$checkout/native"
  : >"$checkout/native/Cargo.toml"
  dir="$ATTN_NATIVE_BUILD_ROOT$checkout/native"
  mkdir -p "$dir/debug/deps" "$dir/debug/.fingerprint"
  : >"$dir/CACHEDIR.TAG"
  : >"$dir/debug/.cargo-build-lock"
  : >"$dir/debug/.cargo-lock"
  head -c $((mib * 1048576)) /dev/zero >"$dir/debug/deps/libexample.rlib"
}

age() {
  local days="$1" dir="$ATTN_NATIVE_BUILD_ROOT$checkouts/$2/native"
  find "$dir" -print0 | xargs -0 perl -e 'my $t = time - shift() * 86400; utime $t, $t, @ARGV' "$days"
}

hold_lock() {
  local name="$2"
  rm -f "$work/locked" "$work/release"
  mkfifo "$work/locked" "$work/release"
  perl -MFcntl=:flock -e '
    open(my $lock, "<", shift) or die;
    flock($lock, LOCK_EX) or die;
    open(my $out, ">", shift) or die; print $out "locked\n"; close $out;
    open(my $in, "<", shift) or die; my $line = <$in>;
  ' "$ATTN_NATIVE_BUILD_ROOT$checkouts/$1/native/debug/$name" "$work/locked" "$work/release" &
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
hold_lock building .cargo-build-lock

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

hold_lock building .cargo-lock
"$root/scripts/native-build-clean" >"$work/out"
present building || fail "removed a build whose lock, as cargo before 1.91 names it, a running build holds"
release_lock

"$root/scripts/native-build-clean" >"$work/out"
present building && fail "kept a stale build after its build ended"
grep -qF "removed $ATTN_NATIVE_BUILD_ROOT$checkouts/building/native" "$work/out" || fail "did not name the stale build it removed once its build ended"
[ -d "$ATTN_NATIVE_BUILD_ROOT$checkouts/fresh/native/debug/.fingerprint" ] || fail "removed an empty directory inside a build it kept"
[ ! -e "$ATTN_NATIVE_BUILD_ROOT$checkouts/building" ] || fail "left the empty directories that held a removed build"

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

rm -rf "$ATTN_NATIVE_BUILD_ROOT" "$checkouts"
build oldest 2
age 2 oldest
build middle 2
age 1 middle
build newest 2
disk_mib=100 other_mib=92 ATTN_NATIVE_BUILD_FREE_FLOOR_MIB=5 "$root/scripts/native-build-clean" >"$work/out" 2>"$work/err"
present oldest && fail "kept the least recently built output while the disk was under its free-space floor"
present middle && fail "stopped removing before the disk was back over its free-space floor"
present newest || fail "removed more than the free-space floor needed"
grep -qF "free-space floor 0.0 GiB" "$work/out" || fail "did not print the free-space floor"
grep -qF "free, under its floor of 0.0 GiB" "$work/out" || fail "did not say the disk was under its free-space floor"
grep -qF "least recently built while the disk has" "$work/out" || fail "did not say why it removed the oldest builds"
[ ! -s "$work/err" ] || fail "warned though it brought the disk back over its floor"

hold_lock newest .cargo-build-lock
disk_mib=100 other_mib=97 ATTN_NATIVE_BUILD_FREE_FLOOR_MIB=5 "$root/scripts/native-build-clean" >"$work/out" 2>"$work/err"
present newest || fail "removed a build in use to reach the free-space floor"
grep -qF "kept $ATTN_NATIVE_BUILD_ROOT$checkouts/newest/native" "$work/out" || fail "did not name the build it kept because it is in use"
grep -qF "still under its free-space floor" "$work/err" || fail "did not warn that the disk stayed under its free-space floor"
release_lock

echo 'native build clean: OK'
