#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/attn-rune-upstream-test.XXXXXX")" && pwd)"
trap 'rm -rf "$work"' EXIT
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

fail() { echo "rune-upstream: $*" >&2; cat "$work/out" >&2; exit 1; }
commit() { git -C "$1" add -A && git -C "$1" commit -qm "$2" && git -C "$1" rev-parse HEAD; }
short() { git -C "$up" rev-parse --short=8 "$1"; }
run() { TMPDIR="$work" RUNE_UPSTREAM="$work/upstream.git" RUNE_FORK="$work/fork.git" RUNE_CLONE="$work/clone" \
  "$root/scripts/rune-upstream" "$@" >"$work/out" 2>&1; }
fork_md() { git -C "$work/fork.git" show main:FORK.md; }

# Upstream has a source file and a folder the fork deletes. The fork's main
# changes the source file's first line and reviewed upstream through its base.
up="$work/upstream-src"
git init -q -b dev "$up"
mkdir -p "$up/src" "$up/gone" "$up/changelog"
seq 1 10 >"$up/src/lib.rs"
echo gone >"$up/gone/old.rs"
base="$(commit "$up" "Rune")"
git clone -q --bare "$up" "$work/upstream.git"
git clone -q --bare "$up" "$work/fork.git"

fork="$work/fork-src"
git clone -q -b dev "$work/fork.git" "$fork"
git -C "$fork" checkout -q -b main
git -C "$fork" rm -rq gone
sed -i.bak '1s/.*/one, ours/' "$fork/src/lib.rs" && rm "$fork/src/lib.rs.bak"
cat >"$fork/FORK.md" <<EOF
# The fork

## Picked from upstream

$(printf 'Last upstream commit reviewed: `%s` (2026-11-12), the last one merged\nbefore the fork became attn'"'"'s own.' "$(short "$base")")

## Dropped

- Nothing.
EOF
commit "$fork" "Fork" >/dev/null
git -C "$fork" push -q origin main
git clone -q "$work/fork.git" "$work/clone"

sed -i.bak '10s/.*/ten, upstream/' "$up/src/lib.rs" && rm "$up/src/lib.rs.bak"
echo "note" >"$up/changelog/clean.md"
clean="$(commit "$up" "Upstream changes the last line")"
mkdir -p "$up/src/new/deeper"
echo new >"$up/src/new/deeper/mod.rs"
added="$(commit "$up" "Upstream adds a folder")"
echo "changed" >"$up/gone/old.rs"
echo "note" >"$up/changelog/gone.md"
gone="$(commit "$up" "Upstream changes a deleted file")"
sed -i.bak '1s/.*/one, upstream/' "$up/src/lib.rs" && rm "$up/src/lib.rs.bak"
clash="$(commit "$up" "Upstream changes the first line")"
git -C "$up" push -q "$work/upstream.git" dev

run list || fail "list failed"
expected="Read through $(short "$clash"), upstream's dev now: 4 commits after $(short "$base"), the last reviewed.

$(short "$clean") $(git -C "$up" log -1 --format=%as "$clean") applies  Upstream changes the last line
    src

$(short "$added") $(git -C "$up" log -1 --format=%as "$added") applies  Upstream adds a folder
    src/new/deeper

$(short "$clash") $(git -C "$up" log -1 --format=%as "$clash") conflicts  Upstream changes the first line
    src

These change only files main has deleted:
  $(short "$gone") $(git -C "$up" log -1 --format=%as "$gone") Upstream changes a deleted file"
[ "$(cat "$work/out")" = "$expected" ] || fail "list printed something else; expected:
$expected"

old_main="$(git -C "$work/fork.git" rev-parse main)"
run reviewed "$clean" || fail "reviewed failed"
git -C "$work/fork.git" merge-base --is-ancestor "$old_main" main || fail "rewrote the fork's main instead of adding to it"
[ "$(git -C "$work/fork.git" rev-list --count "$old_main..main")" = 1 ] || fail "did not add one commit to main"
[ "$(git -C "$work/fork.git" diff --name-only "$old_main" main)" = FORK.md ] || fail "changed more than FORK.md"
fork_md | grep -qF "Last upstream commit reviewed: \`$(short "$clean")\` ($(git -C "$up" log -1 --format=%as "$clean"))." ||
  fail "FORK.md does not name the reviewed commit: $(fork_md)"
fork_md | grep -qF "before the fork became" && fail "kept the old line's continuation"
fork_md | grep -qF -- "- Nothing." || fail "lost the rest of FORK.md"

run list || fail "list after reviewed failed"
head -1 "$work/out" | grep -qF "3 commits after $(short "$clean")" || fail "list did not start after the reviewed commit"

run reviewed "$base" && fail "moved the line back to an earlier commit"
grep -qF "is not after $(short "$clean")" "$work/out" || fail "refused an earlier commit without saying why"

run reviewed "$old_main" && fail "accepted a commit that is not on upstream's dev"
grep -qF "is not a commit on upstream's dev" "$work/out" || fail "refused a fork commit without saying why"

moved="$(git -C "$work/fork.git" rev-parse main)"
run reviewed "$clean" || fail "naming the already reviewed commit failed"
[ "$(git -C "$work/fork.git" rev-parse main)" = "$moved" ] || fail "committed again for the already reviewed commit"

echo "rune-upstream tests passed"
