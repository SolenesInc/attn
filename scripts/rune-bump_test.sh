#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
base="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-rune-bump-test.XXXXXX")" && pwd)"
trap 'rm -rf "$base"' EXIT
PATH="$(rustc --print sysroot)/bin:$PATH"
export CARGO_HOME="$base/cargo-home"
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

case_name=""
api='pub fn answer() -> u32 { 42 }
pub fn greeting() -> &'"'"'static str { "hi" }'
fail() { echo "rune-bump ($case_name): $*" >&2; cat "$work/out" >&2; exit 1; }

commit() { git -C "$1" add -A && git -C "$1" commit -qm "$2"; }

write_crate() {
  local dir="$1" name="$2" body="$3"
  mkdir -p "$dir/crates/$name/src"
  printf '[package]\nname = "%s"\nversion = "0.1.0"\nedition = "2021"\n' "$name" >"$dir/crates/$name/Cargo.toml"
  printf '%s\n' "$body" >"$dir/crates/$name/src/lib.rs"
}

# A Rune with two crates and a [patch.crates-io] that patches nothing it uses,
# mirrored into a fork whose attn branch carries one patch per crate.
setup() {
  case_name="$1"
  work="$base/$1"
  mkdir -p "$work"
  : >"$work/out"
  local up="$work/upstream-src"
  git init -q -b dev "$up"
  printf '[workspace]\nmembers = ["crates/*"]\nexclude = ["vendor"]\nresolver = "2"\n\n[patch.crates-io]\nwinit = { path = "vendor/winit" }\n' >"$up/Cargo.toml"
  write_crate "$up" rune 'mod api;
pub use api::*;'
  printf '%s\n' "$api" >"$up/crates/rune/src/api.rs"
  write_crate "$up" rune-kit 'pub fn kit() -> u32 { 1 }'
  mkdir -p "$up/vendor/winit/src"
  printf '[package]\nname = "winit"\nversion = "0.30.0"\nedition = "2021"\n' >"$up/vendor/winit/Cargo.toml"
  : >"$up/vendor/winit/src/lib.rs"
  commit "$up" "Rune"
  git clone -q --bare "$up" "$work/upstream.git"
  git clone -q --bare "$up" "$work/fork.git"

  local patch="$work/patch"
  git clone -q -b dev "$work/fork.git" "$patch"
  git -C "$patch" checkout -q -b attn
  cat >>"$patch/crates/rune/src/lib.rs" <<'EOF'
#[cfg(test)]
mod tests {
    #[test]
    fn the_answer_holds() { assert_eq!(super::answer(), 42); }
    #[cfg(target_os = "macos")]
    #[test]
    fn only_on_macos() {}
}
EOF
  cat >>"$patch/crates/rune-kit/src/lib.rs" <<'EOF'
#[cfg(test)]
mod tests {
    #[test]
    fn the_kit_holds() { assert_eq!(super::kit(), 1); }
}
EOF
  cat >"$patch/FORK.md" <<'EOF'
| Patch | Why the app cannot do it | Test |
|---|---|---|
| The answer stays 42 (`rune/src/lib.rs`) | The answer is Rune's | `tests::the_answer_holds` |
| The kit holds | Inside `Kit \| Menu` | `tests::the_kit_holds`, `tests::only_on_macos` (macOS) |
EOF
  commit "$patch" "Patch the answer"
  git -C "$patch" push -q origin attn
  old_attn="$(git -C "$patch" rev-parse HEAD)"

  git clone -q "$work/fork.git" "$work/clone"

  attn="$work/attn"
  mkdir -p "$attn/scripts"
  git init -q -b main "$attn"
  cp "$root/scripts/rune-bump" "$attn/scripts/"
  local source="file://$work/fork.git"
  mkdir -p "$attn/native"
  printf '[workspace]\nmembers = ["crates/*"]\nresolver = "2"\n\n[workspace.dependencies]\nrune = { git = "%s", rev = "%s" }\nrune-kit = { git = "%s", rev = "%s" }\n\n[patch.crates-io]\nwinit = { git = "%s", rev = "%s" }\n' \
    "$source" "$old_attn" "$source" "$old_attn" "$source" "$old_attn" >"$attn/native/Cargo.toml"
  write_crate "$attn/native" app '#[test]
fn greets() { assert_eq!(rune::greeting(), "hi"); assert_eq!(rune_kit::kit(), 1); }'
  printf '\n[dependencies]\nrune = { workspace = true }\nrune-kit = { workspace = true }\n' >>"$attn/native/crates/app/Cargo.toml"
  printf 'test-native:\n\tcd native && cargo test --workspace --locked\n' >"$attn/Makefile"
  (cd "$attn/native" && cargo generate-lockfile -q)
  commit "$attn" "attn"
}

upstream_commit() {
  local up="$work/upstream-src"
  [ -z "${2-}" ] || printf '%s\n' "$2" >"$up/crates/rune/src/api.rs"
  [ -z "${3-}" ] || printf '%s\n' "$3" >>"$up/Cargo.toml"
  commit "$up" "$1"
  git -C "$up" push -q "$work/upstream.git" dev
}

bump() {
  (cd "$work/attn" && TMPDIR="$work" RUNE_UPSTREAM="$work/upstream.git" RUNE_FORK="$work/fork.git" RUNE_CLONE="$work/clone" \
    scripts/rune-bump "$@") >"$work/out" 2>&1
}

fork_rev() { git -C "$work/fork.git" rev-parse "$1"; }
pinned() { grep -oE 'rev = "[0-9a-f]{40}"' "$work/attn/native/Cargo.toml" | cut -d'"' -f2 | sort -u; }
pin_unchanged() { git -C "$work/attn" diff --quiet; }

setup merge
upstream_commit "Upstream moves on" "$api
pub fn newer() {}"
bump || fail "a clean bump failed"
[ "$(fork_rev dev)" = "$(git -C "$work/upstream.git" rev-parse dev)" ] ||
  fail "did not fast-forward the fork's dev to upstream's"
git -C "$work/fork.git" merge-base --is-ancestor "$old_attn" attn || fail "rewrote the fork's attn instead of merging"
git -C "$work/fork.git" merge-base --is-ancestor dev attn || fail "did not merge upstream's dev into the fork's attn"
[ "$(pinned)" = "$(fork_rev attn)" ] || fail "did not pin the merged attn"
grep -qF "$(fork_rev attn)" "$work/attn/native/Cargo.lock" || fail "did not move native/Cargo.lock to the merged attn"
[ -n "$(git -C "$work/attn" status --porcelain -- native/Cargo.toml native/Cargo.lock)" ] || fail "committed the pin move"
grep -qF "ok tests::the_answer_holds" "$work/out" || fail "did not run the patch tests FORK.md lists"
grep -qF "ok tests::the_kit_holds" "$work/out" || fail "did not run every test of a patch FORK.md lists"
if [ "$(uname -s)" = Darwin ]; then
  grep -qF "ok tests::only_on_macos" "$work/out" || fail "skipped a macOS test on macOS"
else
  grep -qF "only_on_macos" "$work/out" && fail "ran a macOS-only test off macOS"
fi
grep -qE "test result: ok. 1 passed" "$work/out" || fail "did not show the native suite's result"
grep -qF "merged 1 commits" "$work/out" || fail "did not say how many upstream commits it merged"

setup broken-patch
upstream_commit "Upstream changes the answer" 'pub fn answer() -> u32 { 41 }
pub fn greeting() -> &'"'"'static str { "hi" }'
bump && fail "landed a merge that fails a patch's test"
[ "$(fork_rev attn)" = "$old_attn" ] || fail "pushed an attn whose patch tests fail"
pin_unchanged || fail "moved the pin after the fork's tests failed"
grep -qF "run the tests FORK.md lists failed" "$work/out" || fail "did not name the step that failed"
grep -qF "assertion" "$work/out" || fail "did not show the failing test's output"

setup stale-ledger
sed -i.bak 's/tests::the_kit_holds/tests::renamed_away/' "$work/patch/FORK.md" && rm "$work/patch/FORK.md.bak"
commit "$work/patch" "Name a test that does not exist"
git -C "$work/patch" push -q origin attn
old_attn="$(fork_rev attn)"
upstream_commit "Upstream moves on" "$api
pub fn newer() {}"
bump && fail "accepted a FORK.md that names a test that does not run"
grep -qF "tests::renamed_away" "$work/out" || fail "did not name the test FORK.md lists but did not run"
[ "$(fork_rev attn)" = "$old_attn" ] || fail "pushed attn though a listed test did not run"

setup patch-section
mkdir -p "$work/upstream-src/vendor/wgpu-hal/src"
printf '[package]\nname = "wgpu-hal"\nversion = "0.1.0"\nedition = "2021"\n' >"$work/upstream-src/vendor/wgpu-hal/Cargo.toml"
: >"$work/upstream-src/vendor/wgpu-hal/src/lib.rs"
upstream_commit "Upstream patches another crate" "" 'wgpu-hal = { path = "vendor/wgpu-hal" }'
bump && fail "moved the pin though Rune's [patch.crates-io] changed"
grep -qF "Rune's [patch.crates-io] at $(fork_rev attn) differs from native/Cargo.toml's" "$work/out" || fail "did not say Rune's [patch.crates-io] changed"
grep -qF "< wgpu-hal" "$work/out" || fail "did not show the [patch.crates-io] difference"
pin_unchanged || fail "moved the pin though Rune's [patch.crates-io] changed"
printf 'wgpu-hal = { git = "file://%s", rev = "%s" }\n' "$work/fork.git" "$old_attn" >>"$work/attn/native/Cargo.toml"
commit "$work/attn" "Repeat Rune's new patch"
bump --rev "$(fork_rev attn)" || fail "--rev failed after native/Cargo.toml repeated Rune's new [patch.crates-io]"
[ "$(pinned)" = "$(fork_rev attn)" ] || fail "did not pin once native/Cargo.toml repeated Rune's new [patch.crates-io]"

setup conflict
printf '%s\n' "$api" 'pub fn patched() {}' >"$work/patch/crates/rune/src/api.rs"
commit "$work/patch" "Patch the api"
git -C "$work/patch" push -q origin attn
old_attn="$(fork_rev attn)"
upstream_commit "Upstream rewrites the api" 'pub fn answer() -> u32 { 42 }
pub fn greeting() -> &'"'"'static str { "hello" }'
bump && fail "reported success though the merge conflicts"
grep -qF "conflicts in: crates/rune/src/api.rs" "$work/out" || fail "did not name the conflicting file"
[ "$(fork_rev attn)" = "$old_attn" ] || fail "pushed attn though the merge conflicts"
pin_unchanged || fail "moved the pin though the merge conflicts"
[ -z "$(git -C "$work/clone" worktree list | sed 1d)" ] || fail "left its worktree in the Rune clone"

setup refusals
echo "# edit" >>"$work/attn/native/Cargo.toml"
bump && fail "moved the pin over uncommitted changes to native/Cargo.toml"
grep -qF "uncommitted changes" "$work/out" || fail "did not say native/ has uncommitted changes"
git -C "$work/attn" checkout -q -- native
git clone -q -b dev "$work/fork.git" "$work/dev-src"
echo "// fork-only" >>"$work/dev-src/crates/rune/src/lib.rs"
commit "$work/dev-src" "Commit on the fork's dev"
git -C "$work/dev-src" push -q origin dev
upstream_commit "Upstream moves on" "$api
pub fn newer() {}"
bump && fail "merged though the fork's dev has commits upstream lacks"
grep -qF "dev only mirrors upstream" "$work/out" || fail "did not say the fork's dev must only mirror upstream"
[ "$(fork_rev attn)" = "$old_attn" ] || fail "pushed attn though the fork's dev diverged"

setup native-red
upstream_commit "Upstream drops the greeting" 'pub fn answer() -> u32 { 42 }'
bump && fail "reported success though the native suite fails"
grep -qF "the native suite fails at $(fork_rev attn)" "$work/out" || fail "did not say the native suite fails at the new rev"
grep -qF "greeting" "$work/out" || fail "did not show the native suite's failure"

setup rev
git clone -q -b attn "$work/fork.git" "$work/rev-src"
echo "// more" >>"$work/rev-src/crates/rune/src/lib.rs"
commit "$work/rev-src" "Fix on attn"
git -C "$work/rev-src" push -q origin attn
bump --rev "$(fork_rev attn)" || fail "--rev failed"
[ "$(pinned)" = "$(fork_rev attn)" ] || fail "--rev did not pin the given revision"
[ "$(fork_rev dev)" = "$(git -C "$work/fork.git" rev-parse "$old_attn^")" ] || fail "--rev touched the fork's dev"
git -C "$work/rev-src" checkout -q -b elsewhere
echo "// elsewhere" >>"$work/rev-src/crates/rune/src/lib.rs"
commit "$work/rev-src" "Off attn"
git -C "$work/rev-src" push -q origin elsewhere
git -C "$work/attn" checkout -q -- native
bump --rev "$(fork_rev elsewhere)" && fail "pinned a revision that is not on the fork's attn"
grep -qF "is not on the fork's attn branch" "$work/out" || fail "did not say the revision is off attn"

setup branch
git clone -q -b attn "$work/fork.git" "$work/branch-src"
git -C "$work/branch-src" checkout -q -b topic
echo "// topic" >>"$work/branch-src/crates/rune/src/lib.rs"
commit "$work/branch-src" "Topic fix"
git -C "$work/branch-src" push -q origin topic
bump --branch topic || fail "--branch failed"
[ "$(fork_rev attn)" = "$(fork_rev topic)" ] || fail "--branch did not bring the branch into attn"
[ "$(pinned)" = "$(fork_rev attn)" ] || fail "--branch did not pin the merged attn"
grep -qF "ok tests::the_answer_holds" "$work/out" || fail "--branch did not run the patch tests"

echo "rune-bump tests passed"
