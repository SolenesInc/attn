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

# A Rune with three crates and a [patch.crates-io] that patches nothing it uses,
# forked into a main branch that carries one change, listed in FORK.md with its
# tests and its commit.
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
  printf '\n[features]\nmarkdown = []\nmermaid = ["markdown"]\n' >>"$up/crates/rune-kit/Cargo.toml"
  write_crate "$up" rune-mermaid 'pub fn shapes() -> u32 { 3 }'
  mkdir -p "$up/vendor/winit/src"
  printf '[package]\nname = "winit"\nversion = "0.30.0"\nedition = "2021"\n' >"$up/vendor/winit/Cargo.toml"
  : >"$up/vendor/winit/src/lib.rs"
  commit "$up" "Rune"
  git clone -q --bare "$up" "$work/upstream.git"
  git clone -q --bare "$up" "$work/fork.git"

  local patch="$work/patch"
  git clone -q -b dev "$work/fork.git" "$patch"
  git -C "$patch" checkout -q -b main
  cat >>"$patch/crates/rune/src/lib.rs" <<'EOF'
#[cfg(test)]
mod tests {
    #[test]
    fn the_answer_holds() { assert_eq!(super::answer(), 42); }
    #[test]
    #[should_panic(expected = "never 41")]
    fn a_wrong_answer_panics() { assert!(super::answer() != 42, "never 41"); }
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
    #[cfg(feature = "mermaid")]
    #[test]
    fn a_diagram_draws() {}
}
EOF
  cat >>"$patch/crates/rune-mermaid/src/lib.rs" <<'EOF'
#[cfg(test)]
mod tests {
    #[test]
    fn the_shapes_hold() { assert_eq!(super::shapes(), 3); }
}
EOF
  cat >"$patch/FORK.md" <<'EOF'
# The fork

## Our changes

| Change | Why it is in Rune | Test | Commits |
|---|---|---|---|
| The answer stays 42 (`rune/src/lib.rs`) | The answer is Rune's | `tests::the_answer_holds`, `tests::a_wrong_answer_panics` | PATCH |
| The kit holds | Inside `Kit \| Menu` | `tests::the_kit_holds`, `tests::only_on_macos` (macOS), `tests::a_diagram_draws` | PATCH |
| The shapes hold | Inside the diagram parser | `tests::the_shapes_hold` (rune-mermaid) | PATCH |

## Picked from upstream

| Upstream commit | Subject |
|---|---|

Last upstream commit reviewed: none.
EOF
  printf 'FORK.md merge=union\n' >"$patch/.gitattributes"
  mkdir -p "$patch/scripts"
  printf '#!/usr/bin/env bash\n: >>"$TMPDIR/through-test-sh"\nexec cargo test "$@"\n' >"$patch/scripts/test.sh"
  chmod +x "$patch/scripts/test.sh"
  commit "$patch" "Patch the answer"
  local sha
  sha="$(git -C "$patch" rev-parse --short=8 HEAD)"
  sed -i.bak "s/PATCH/\`$sha\`/" "$patch/FORK.md" && rm "$patch/FORK.md.bak"
  commit "$patch" "Name the patch's commit in FORK.md"
  git -C "$patch" push -q origin main
  old_main="$(git -C "$patch" rev-parse HEAD)"

  git clone -q "$work/fork.git" "$work/clone"

  attn="$work/attn"
  mkdir -p "$attn/scripts"
  git init -q -b main "$attn"
  cp "$root/scripts/rune-bump" "$attn/scripts/"
  local source="file://$work/fork.git"
  mkdir -p "$attn/native"
  printf '[workspace]\nmembers = ["crates/*"]\nresolver = "2"\n\n[workspace.dependencies]\nrune = { git = "%s", rev = "%s" }\nrune-kit = { git = "%s", rev = "%s" }\n\n[patch.crates-io]\nwinit = { git = "%s", rev = "%s" }\n' \
    "$source" "$old_main" "$source" "$old_main" "$source" "$old_main" >"$attn/native/Cargo.toml"
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

upstream_commit() {
  local up="$work/upstream-src"
  [ -z "${2-}" ] || printf '%s\n' "$2" >"$up/crates/rune/src/api.rs"
  [ -z "${3-}" ] || printf '%s\n' "$3" >>"$up/Cargo.toml"
  commit "$up" "$1"
  git -C "$up" push -q "$work/upstream.git" dev
  git -C "$up" rev-parse HEAD
}

# Adds a commit to the Commits cell of the first row of FORK.md's "Our changes".
list_commit() {
  sed -i.bak "0,/\` |\$/s//\`, \`$2\` |/" "$1/FORK.md" && rm "$1/FORK.md.bak"
  grep -qF "\`$2\` |" "$1/FORK.md" || { echo "list_commit could not list $2" >&2; exit 1; }
}

# Commits a change to the fork's main through a clone of it. Unless told not
# to, the same commit extends the first row of FORK.md's "Our changes".
fork_change() {
  local src="$work/main-src"
  [ -d "$src" ] || git clone -q -b main "$work/fork.git" "$src"
  git -C "$src" pull -q --ff-only
  echo "// $1" >>"$src/crates/rune/src/lib.rs"
  if [ "${2-}" != unlisted ]; then
    sed -i.bak "s/^| The answer stays 42/| The answer stays 42, and $1/" "$src/FORK.md" && rm "$src/FORK.md.bak"
  fi
  commit "$src" "$1"
  git -C "$src" push -q origin main
}

bump() {
  (cd "$work/attn" && TMPDIR="$work" RUNE_UPSTREAM="$work/upstream.git" RUNE_FORK="$work/fork.git" RUNE_CLONE="$work/clone" \
    scripts/rune-bump "$@") >"$work/out" 2>&1
}

fork_rev() { git -C "$work/fork.git" rev-parse "$1"; }
fork_md() { git -C "$work/fork.git" show main:FORK.md; }
pinned() { grep -oE 'rev = "[0-9a-f]{40}"' "$work/attn/native/Cargo.toml" | cut -d'"' -f2 | sort -u; }
pin_unchanged() { git -C "$work/attn" diff --quiet; }

setup pick
first="$(upstream_commit "Upstream adds newer" "$api
pub fn newer() {}")"
second="$(upstream_commit "Upstream adds newest" "$api
pub fn newer() {}
pub fn newest() {}")"
bump --pick "$first" "$second" || fail "a clean pick failed"
[ "$(fork_rev dev)" = "$second" ] || fail "did not fast-forward the fork's dev to upstream's"
git -C "$work/fork.git" merge-base --is-ancestor "$old_main" main || fail "rewrote the fork's main instead of adding to it"
git -C "$work/fork.git" merge-base --is-ancestor dev main && fail "merged upstream's dev into main instead of picking"
[ "$(git -C "$work/fork.git" rev-list --count "$old_main..main")" = 2 ] || fail "did not add one commit per pick"
git -C "$work/fork.git" log -1 --format=%B main | grep -qxF "(cherry picked from commit $second)" ||
  fail "a pick does not name the upstream commit it took"
fork_md | grep -qF "| \`${first:0:8}\` | Upstream adds newer |" || fail "did not record the first pick in FORK.md"
fork_md | grep -qF "| \`${second:0:8}\` | Upstream adds newest |" || fail "did not record the second pick in FORK.md"
fork_md | sed -n '/^## Picked from upstream/,$p' | grep -qF "${first:0:8}" || fail "recorded the pick outside \"Picked from upstream\""
[ "$(pinned)" = "$(fork_rev main)" ] || fail "did not pin the new main"
grep -qF "$(fork_rev main)" "$work/attn/native/Cargo.lock" || fail "did not move native/Cargo.lock to the new main"
[ -n "$(git -C "$work/attn" status --porcelain -- native/Cargo.toml native/Cargo.lock)" ] || fail "committed the pin move"
grep -qF "ok tests::the_answer_holds" "$work/out" || fail "did not run the tests FORK.md lists"
[ -f "$work/through-test-sh" ] || fail "ran the fork's tests without its scripts/test.sh"
grep -qF "ok tests::the_kit_holds" "$work/out" || fail "did not run every test of a row FORK.md lists"
grep -qF "ok tests::a_wrong_answer_panics" "$work/out" || fail "missed a should_panic test FORK.md lists"
grep -qF "ok tests::the_shapes_hold" "$work/out" || fail "missed a rune-mermaid test FORK.md lists"
if [ "$(uname -s)" = Darwin ]; then
  grep -qF "ok tests::only_on_macos" "$work/out" || fail "skipped a macOS test on macOS"
else
  grep -qF "only_on_macos" "$work/out" && fail "ran a macOS-only test off macOS"
fi
grep -qE "test result: ok. 1 passed" "$work/out" || fail "did not show the native suite's result"

setup broken-patch
broken="$(upstream_commit "Upstream changes the answer" 'pub fn answer() -> u32 { 41 }
pub fn greeting() -> &'"'"'static str { "hi" }')"
bump --pick "$broken" && fail "landed a pick that fails a listed test"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed a main whose listed tests fail"
pin_unchanged || fail "moved the pin after the fork's tests failed"
grep -qF "run the tests FORK.md lists failed" "$work/out" || fail "did not name the step that failed"
grep -qF "assertion" "$work/out" || fail "did not show the failing test's output"

setup stale-ledger
sed -i.bak 's/tests::the_kit_holds/tests::renamed_away/' "$work/patch/FORK.md" && rm "$work/patch/FORK.md.bak"
commit "$work/patch" "Name a test that does not exist"
git -C "$work/patch" push -q origin main
old_main="$(fork_rev main)"
bump --pick "$(upstream_commit "Upstream moves on" "$api
pub fn newer() {}")" && fail "accepted a FORK.md that names a test that does not run"
grep -qF "tests::renamed_away" "$work/out" || fail "did not name the test FORK.md lists but did not run"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed main though a listed test did not run"

setup unlisted
fork_change "Listed fix"
git -C "$work/main-src" checkout -q -b side
echo "// side" >>"$work/main-src/crates/rune/src/lib.rs"
commit "$work/main-src" "Side fix"
git -C "$work/main-src" checkout -q main
git -C "$work/main-src" cherry-pick -x side >/dev/null
list_commit "$work/main-src" "$(git -C "$work/main-src" rev-parse --short=8 HEAD)"
commit "$work/main-src" "List the side fix"
git -C "$work/main-src" push -q origin main
bump --rev "$(fork_rev main)" || fail "refused a main whose commits FORK.md lists, one of them picked from a fork branch"
git -C "$work/attn" checkout -q -- native
fork_change "Unlisted fix" unlisted
bump --rev "$(fork_rev main)" && fail "pinned a main with a commit FORK.md does not list"
grep -qF "records none of these commits" "$work/out" || fail "did not say FORK.md does not record a commit"
grep -qF "Unlisted fix" "$work/out" || fail "did not name the commit FORK.md lacks"
grep -qF "Listed fix" "$work/out" && fail "named a commit FORK.md lists"
pin_unchanged || fail "moved the pin though FORK.md lacks a row"
git -C "$work/main-src" reset -q --hard HEAD^
picked="$(upstream_commit "Upstream adds newer" "$api
pub fn newer() {}")"
git -C "$work/main-src" fetch -q "$work/upstream.git" dev
git -C "$work/main-src" cherry-pick -x "$picked" >/dev/null
git -C "$work/main-src" push -q -f origin main
bump --rev "$(fork_rev main)" && fail "pinned a main with a pick FORK.md does not record"
grep -qF "Upstream adds newer" "$work/out" || fail "did not name the pick FORK.md lacks"

setup patch-section
mkdir -p "$work/upstream-src/vendor/wgpu-hal/src"
printf '[package]\nname = "wgpu-hal"\nversion = "0.1.0"\nedition = "2021"\n' >"$work/upstream-src/vendor/wgpu-hal/Cargo.toml"
: >"$work/upstream-src/vendor/wgpu-hal/src/lib.rs"
bump --pick "$(upstream_commit "Upstream patches another crate" "" 'wgpu-hal = { path = "vendor/wgpu-hal" }')" &&
  fail "moved the pin though Rune's [patch.crates-io] changed"
grep -qF "Rune's [patch.crates-io] at $(fork_rev main) differs from native/Cargo.toml's" "$work/out" || fail "did not say Rune's [patch.crates-io] changed"
grep -qF "< wgpu-hal" "$work/out" || fail "did not show the [patch.crates-io] difference"
pin_unchanged || fail "moved the pin though Rune's [patch.crates-io] changed"
printf 'wgpu-hal = { git = "file://%s", rev = "%s" }\n' "$work/fork.git" "$old_main" >>"$work/attn/native/Cargo.toml"
commit "$work/attn" "Repeat Rune's new patch"
bump --rev "$(fork_rev main)" || fail "--rev failed after native/Cargo.toml repeated Rune's new [patch.crates-io]"
[ "$(pinned)" = "$(fork_rev main)" ] || fail "did not pin once native/Cargo.toml repeated Rune's new [patch.crates-io]"

setup conflict
printf '%s\n' "$api" 'pub fn patched() {}' >"$work/patch/crates/rune/src/api.rs"
commit "$work/patch" "Patch the api"
list_commit "$work/patch" "$(git -C "$work/patch" rev-parse --short=8 HEAD)"
commit "$work/patch" "List the api patch"
git -C "$work/patch" push -q origin main
old_main="$(fork_rev main)"
bump --pick "$(upstream_commit "Upstream rewrites the api" 'pub fn answer() -> u32 { 42 }
pub fn greeting() -> &'"'"'static str { "hello" }')" && fail "reported success though the pick conflicts"
grep -qF "conflicts in: crates/rune/src/api.rs" "$work/out" || fail "did not name the conflicting file"
grep -qF "port it by hand" "$work/out" || fail "did not say how to take a pick that conflicts"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed main though the pick conflicts"
pin_unchanged || fail "moved the pin though the pick conflicts"
[ -z "$(git -C "$work/clone" worktree list | sed 1d)" ] || fail "left its worktree in the Rune clone"

setup refusals
bump && fail "ran without a mode"
grep -qF "usage: scripts/rune-bump --pick" "$work/out" || fail "did not show the usage without a mode"
echo "# edit" >>"$work/attn/native/Cargo.toml"
bump --rev "$old_main" && fail "moved the pin over uncommitted changes to native/Cargo.toml"
grep -qF "uncommitted changes" "$work/out" || fail "did not say native/ has uncommitted changes"
git -C "$work/attn" checkout -q -- native
bump --pick "$old_main" && fail "picked a commit that is not on upstream's dev"
grep -qF "not a commit on upstream's dev" "$work/out" || fail "did not say the pick is not upstream's"
git clone -q -b dev "$work/fork.git" "$work/dev-src"
echo "// fork-only" >>"$work/dev-src/crates/rune/src/lib.rs"
commit "$work/dev-src" "Commit on the fork's dev"
git -C "$work/dev-src" push -q origin dev
bump --pick "$(upstream_commit "Upstream moves on" "$api
pub fn newer() {}")" && fail "picked though the fork's dev has commits upstream lacks"
grep -qF "dev only mirrors upstream" "$work/out" || fail "did not say the fork's dev must only mirror upstream"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed main though the fork's dev diverged"

setup native-red
bump --pick "$(upstream_commit "Upstream drops the greeting" 'pub fn answer() -> u32 { 42 }')" &&
  fail "reported success though the native suite fails"
grep -qF "the native suite fails at $(fork_rev main)" "$work/out" || fail "did not say the native suite fails at the new rev"
grep -qF "greeting" "$work/out" || fail "did not show the native suite's failure"

setup rev
fork_change "Fix on main"
bump --rev "$(fork_rev main)" || fail "--rev failed"
[ "$(pinned)" = "$(fork_rev main)" ] || fail "--rev did not pin the given revision"
[ "$(fork_rev dev)" = "$(git -C "$work/upstream.git" rev-parse dev)" ] || fail "--rev touched the fork's dev"
git -C "$work/main-src" checkout -q -b elsewhere
echo "// elsewhere" >>"$work/main-src/crates/rune/src/lib.rs"
commit "$work/main-src" "Off main"
git -C "$work/main-src" push -q origin elsewhere
git -C "$work/attn" checkout -q -- native
bump --rev "$(fork_rev elsewhere)" && fail "pinned a revision that is not on the fork's main"
grep -qF "is not on the fork's main" "$work/out" || fail "did not say the revision is off main"

setup branch
git clone -q -b main "$work/fork.git" "$work/branch-src"
git -C "$work/branch-src" checkout -q -b topic
echo "// topic" >>"$work/branch-src/crates/rune/src/lib.rs"
commit "$work/branch-src" "Topic fix"
git -C "$work/branch-src" push -q origin topic
bump --branch topic && fail "merged a branch whose commit FORK.md does not list"
grep -qF "Topic fix" "$work/out" || fail "did not name the branch's unlisted commit"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed main though the branch's commit has no row"
list_commit "$work/branch-src" "$(git -C "$work/branch-src" rev-parse --short=8 HEAD)"
commit "$work/branch-src" "List the topic fix"
git -C "$work/branch-src" push -q origin topic
bump --branch topic || fail "--branch failed"
git -C "$work/fork.git" merge-base --is-ancestor topic main || fail "--branch did not bring the branch into main"
git -C "$work/fork.git" merge-base --is-ancestor "$old_main" main || fail "--branch rewrote main"
[ "$(pinned)" = "$(fork_rev main)" ] || fail "--branch did not pin the new main"
grep -qF "ok tests::the_answer_holds" "$work/out" || fail "--branch did not run the listed tests"
upstream_commit "Upstream moves on" "$api
pub fn newer() {}" >/dev/null
git -C "$work/branch-src" fetch -q "$work/upstream.git" dev
git -C "$work/branch-src" merge -q --no-edit FETCH_HEAD
git -C "$work/branch-src" push -q origin topic
git -C "$work/attn" checkout -q -- native
merged_main="$(fork_rev main)"
bump --branch topic && fail "merged a branch that merged upstream's dev"
grep -qF "carries commits of upstream's dev that main lacks" "$work/out" || fail "did not say the branch merged upstream"
[ "$(fork_rev main)" = "$merged_main" ] || fail "pushed main though the branch merged upstream"

setup integration
src="$work/integration-src"
git clone -q -b main "$work/fork.git" "$src"
git -C "$src" checkout -q -b outside
mkdir -p "$src/crates/rune/tests" "$src/crates/rune-kit/tests/kit_from_outside"
printf '#[test]\nfn the_answer_holds_from_outside() { assert_eq!(rune::answer(), 42); }\n#[test]\nfn the_greeting_holds_from_outside() { assert_eq!(rune::greeting(), "hi"); }\n' >"$src/crates/rune/tests/outside.rs"
printf '#[cfg(feature = "mermaid")]\n#[test]\nfn a_diagram_draws_from_outside() { assert_eq!(rune_kit::kit(), 1); }\n' >"$src/crates/rune-kit/tests/kit_from_outside/main.rs"
cat >>"$src/FORK.md.row" <<'EOF2'
| Rune answers apps from outside | The answer is Rune's | `crates/rune/tests/outside.rs::the_answer_holds_from_outside`, `crates/rune-kit/tests/kit_from_outside/main.rs` | |
EOF2
sed -i.bak '/^| The shapes hold/r '"$src/FORK.md.row" "$src/FORK.md" && rm "$src/FORK.md.bak" "$src/FORK.md.row"
commit "$src" "Test Rune from outside"
git -C "$src" push -q origin outside
bump --branch outside || fail "refused integration tests FORK.md names"
grep -qF "ok crates/rune/tests/outside.rs::the_answer_holds_from_outside" "$work/out" || fail "did not run an integration test FORK.md names"
grep -qF "ok crates/rune-kit/tests/kit_from_outside/main.rs" "$work/out" || fail "did not run an integration test file FORK.md names"
grep -qF "ok tests::the_answer_holds" "$work/out" || fail "skipped the unit tests beside an integration test"
[ "$(pinned)" = "$(fork_rev main)" ] || fail "did not pin a main whose integration tests pass"
git -C "$work/attn" checkout -q -- native
sed -i.bak 's/outside.rs::the_answer_holds_from_outside/outside.rs::renamed_away/; s/kit_from_outside\/main.rs/gone.rs/' "$src/FORK.md" && rm "$src/FORK.md.bak"
commit "$src" "Name integration tests that do not exist"
git -C "$src" push -q origin outside
old_main="$(fork_rev main)"
bump --branch outside && fail "accepted integration tests FORK.md names that do not run"
grep -qF "crates/rune/tests/outside.rs::renamed_away" "$work/out" || fail "did not name the integration test that did not run"
grep -qF "crates/rune-kit/tests/gone.rs" "$work/out" || fail "did not name the integration test file that does not exist"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed main though a listed integration test did not run"

setup parallel-rows
for branch in left right; do
  src="$work/$branch-src"
  git clone -q -b main "$work/fork.git" "$src"
  git -C "$src" checkout -q -b "$branch"
  echo "// $branch" >>"$src/crates/rune-mermaid/src/$branch.rs"
  printf '| The %s change | Inside Rune | `tests::the_shapes_hold` | |\n' "$branch" >"$src/row"
  sed -i.bak '/^| The shapes hold/r '"$src/row" "$src/FORK.md" && rm "$src/FORK.md.bak" "$src/row"
  commit "$src" "The $branch change"
  git -C "$src" push -q origin "$branch"
done
bump --branch left || fail "--branch failed for the first of two branches that each add a FORK.md row"
git -C "$work/attn" checkout -q -- native
bump --branch right || fail "the second of two branches that each add a FORK.md row did not merge"
fork_md | grep -qF "| The left change |" || fail "lost the first branch's FORK.md row"
fork_md | grep -qF "| The right change |" || fail "lost the second branch's FORK.md row"
git -C "$work/fork.git" merge-base --is-ancestor right main || fail "did not bring the second branch into main"

setup parallel-edits
for branch in left right; do
  src="$work/$branch-src"
  git clone -q -b main "$work/fork.git" "$src"
  git -C "$src" checkout -q -b "$branch"
  echo "// $branch" >>"$src/crates/rune-mermaid/src/$branch.rs"
  sed -i.bak "s/^| The answer stays 42/| The answer stays 42, and $branch/; s/^Last upstream commit reviewed: none./Last upstream commit reviewed: $branch./" "$src/FORK.md" && rm "$src/FORK.md.bak"
  commit "$src" "The $branch change"
  git -C "$src" push -q origin "$branch"
done
bump --branch left || fail "--branch failed for the first of two branches that extend one FORK.md row"
git -C "$work/attn" checkout -q -- native
old_main="$(fork_rev main)"
bump --branch right && fail "merged a second branch that extends the same FORK.md row, leaving the row twice"
grep -qF "| The answer stays 42, and left (" "$work/out" || fail "did not name main's version of the row both branches extended"
grep -qF "| The answer stays 42, and right (" "$work/out" || fail "did not name the branch's version of the row both branches extended"
grep -qF "Last upstream commit reviewed: right." "$work/out" || fail "did not name the reviewed-upstream line both branches moved"
[ "$(fork_rev main)" = "$old_main" ] || fail "pushed main with a FORK.md row twice"
pin_unchanged || fail "moved the pin though FORK.md has a row twice"
src="$work/right-src"
git -C "$src" pull -q --no-rebase --no-edit origin main 2>/dev/null
grep -qF "| The answer stays 42, and right (" "$src/FORK.md" || fail "the union merge of main into the branch lost a row"
sed -i.bak '/^| The answer stays 42, and left (/d; s/^| The answer stays 42, and right/| The answer stays 42, and left, and right/; /^Last upstream commit reviewed: left./d' "$src/FORK.md" && rm "$src/FORK.md.bak"
commit "$src" "Make the doubled FORK.md lines one"
git -C "$src" push -q origin right
bump --branch right || fail "refused a branch whose later commit made the doubled FORK.md lines one"

setup same-deletion
for branch in left right; do
  src="$work/$branch-src"
  git clone -q -b main "$work/fork.git" "$src"
  git -C "$src" checkout -q -b "$branch"
  echo "// $branch" >>"$src/crates/rune-mermaid/src/$branch.rs"
  sed -i.bak '/^| The shapes hold/d' "$src/FORK.md" && rm "$src/FORK.md.bak"
  commit "$src" "The $branch change drops the shapes row"
  git -C "$src" push -q origin "$branch"
done
bump --branch left || fail "--branch failed for the first of two branches that drop one FORK.md row"
git -C "$work/attn" checkout -q -- native
bump --branch right || fail "refused a second branch that drops the same FORK.md row"

echo "rune-bump tests passed"
