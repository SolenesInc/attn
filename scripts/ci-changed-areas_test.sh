#!/usr/bin/env bash
# Tests for ci-changed-areas.sh, the CI "Changes" job's classifier.
#
# Every gated job keys off its output, so the contract is: the right areas for
# an ordinary diff, and every area when the base is unusable. The second half
# is the one that strands the suite when it breaks. Run: bash scripts/ci-changed-areas_test.sh
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/ci-changed-areas.sh"

pass=0
fail=0

check() {
  local label="$1" want="$2" got="$3"
  if [[ "$want" == "$got" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $label"
    echo "  want: $(tr '\n' ' ' <<<"$want")"
    echo "  got:  $(tr '\n' ' ' <<<"$got")"
  fi
}

all_true=$'daemon=true\nfrontend=true\ngenerated=true\ntauri=true\nplugins=true'

sandbox="$(mktemp -d "${TMPDIR:-/tmp}/ci-changed-areas-test.XXXXXX")"
trap 'rm -rf "$sandbox"' EXIT
repo="$sandbox/repo"
git init -q -b main "$repo"
g() { git -C "$repo" -c user.name=t -c user.email=t@t "$@"; }

commit_file() {
  mkdir -p "$repo/$(dirname "$1")"
  echo "$2" >"$repo/$1"
  g add -A && g commit -q -m "$1"
}

commit_file README.md base
base="$(g rev-parse HEAD)"
commit_file internal/daemon/x.go one
commit_file docs/notes.md two
head="$(g rev-parse HEAD)"

run() { (cd "$repo" && "$script" "$@"); }

check "go file under internal is daemon only" \
  $'daemon=true\nfrontend=false\ngenerated=false\ntauri=false\nplugins=false' \
  "$(run "$base" "$head")"

# The base moved on after the branch forked; only the branch's own commits count.
g checkout -q -b other "$base"
commit_file app/src/main.tsx three
g checkout -q main
check "diff is against the merge-base, not the base tip" \
  $'daemon=true\nfrontend=false\ngenerated=false\ntauri=false\nplugins=false' \
  "$(run other "$head")"

check "no diff is every area false" \
  $'daemon=false\nfrontend=false\ngenerated=false\ntauri=false\nplugins=false' \
  "$(run "$head" "$head")"

check "empty base is every area" "$all_true" "$(run "")"
check "all-zero base is every area" "$all_true" \
  "$(run 0000000000000000000000000000000000000000)"
check "unknown base sha is every area" "$all_true" \
  "$(run 1111111111111111111111111111111111111111 "$head")"

g checkout -q --orphan orphan && g rm -q -rf . && commit_file lonely.txt x
orphan="$(g rev-parse HEAD)"
check "unrelated history is every area" "$all_true" "$(run "$orphan" "$head")"

status=0
(cd "$repo" && "$script" "$orphan" "$head" >/dev/null) || status=$?
check "unusable base still exits 0" 0 "$status"

echo "passed=$pass failed=$fail"
[[ $fail -eq 0 ]]
