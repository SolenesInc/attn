#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/repo"
cat >"$work/bin/make" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" >>"$LAND_CHECK_LOG"
if [ -e "$LAND_MOVE_MAIN" ]; then
  rm "$LAND_MOVE_MAIN"
  git -C "$LAND_OTHER" pull -q origin main
  git -C "$LAND_OTHER" commit -q --allow-empty -m 'Landed during the gate'
  git -C "$LAND_OTHER" push -q origin HEAD:main
fi
exit "${LAND_CHECK_EXIT:-0}"
EOF
chmod +x "$work/bin/make"
cat >"$work/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "$1 $2" = "tool test-history" ] || { echo "unexpected go $*" >&2; exit 2; }
printf '%s\n' "$3" >>"$LAND_CHECK_LOG"
case "$3" in
  sync) exit "${LAND_SYNC_EXIT:-0}" ;;
  land)
    printf '%s\n' "${@:6}" >>"$LAND_CHECK_LOG"
    [ "${LAND_SELECT_EXIT:-0}" -eq 0 ] || exit "$LAND_SELECT_EXIT"
    [ "$4" = --units-out ] || exit 2
    printf '%s' "${LAND_SELECTED-$selected}" >"$5"
    ;;
esac
EOF
chmod +x "$work/bin/go"
export PATH="$work/bin:$PATH"
export LAND_CHECK_LOG="$work/checks" LAND_MOVE_MAIN="$work/move-main" LAND_OTHER="$work/other"
export selected=$'go ./internal/example TestExample\nvitest src/example.test.ts'
selected_tests=(landing-tests --unit 'go ./internal/example TestExample' --unit 'vitest src/example.test.ts')
go_choice=(land --group go --group prompts "${selected_tests[@]}")
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git init -q --bare "$work/remote"
cd "$work/repo"
git init -q
git config user.name 'Landing Test'
git config user.email 'landing@example.invalid'
git config core.hooksPath /dev/null
git switch -qc work
mkdir -p internal/example internal/removed app/src scripts internal/protocol/schema internal/prompts
printf 'package example\n' >internal/example/example.go
printf 'package example\n' >internal/example/other.go
printf 'package removed\n' >internal/removed/removed.go
printf 'module example.invalid/landing\n' >go.mod
printf 'baseline\n' >README.md
cp "$root/scripts/landing-tests" scripts/landing-tests
cat >scripts/landing-tests.sh <<'EOF'
#!/usr/bin/env bash
printf '%s\n' landing-tests "$@" >>"$LAND_CHECK_LOG"
exit "${LAND_TESTS_EXIT:-0}"
EOF
chmod +x scripts/landing-tests.sh
git add .
git commit -qm baseline
git remote add origin "$work/remote"
git push -q origin HEAD:main
git clone -q -b main "$work/remote" "$LAND_OTHER"
git -C "$LAND_OTHER" config user.name 'Other Landing'
git -C "$LAND_OTHER" config user.email 'other@example.invalid'

land() {
  local message="$1"
  shift
  : >"$LAND_CHECK_LOG"
  git add -A
  git commit -qm "$message"
  env "$@" bash "$root/scripts/land.sh" ${LAND_FLAGS:-} >"$work/output" 2>&1 || { cat "$work/output" >&2; return 1; }
  [ "$(git rev-parse HEAD)" = "$(git --git-dir="$work/remote" rev-parse main)" ]
}

expect_output() {
  grep -qF -- "$1" "$work/output" || { echo "missing from the landing output: $1" >&2; cat "$work/output" >&2; exit 1; }
}

expect_checks() {
  printf '%s\n' "$@" >"$work/expected"
  diff -u "$work/expected" "$LAND_CHECK_LOG"
}

printf '\n' >>internal/example/example.go
land 'Go change'
expect_checks sync check-go 'GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

git rm -q internal/example/other.go internal/removed/removed.go
land 'Go deletions'
expect_checks sync check-go 'GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

printf '\n' >>go.mod
land 'Module change'
expect_checks sync check-go 'GO_PACKAGES=./...' "${go_choice[@]}" sync

mkdir -p app/src
printf 'export {};\n' >app/src/example.ts
land 'App change'
expect_checks sync check-frontend land --group app "${selected_tests[@]}" sync

mkdir -p scripts internal/protocol/schema internal/prompts
printf 'true\n' >scripts/example.sh
printf 'schema\n' >internal/protocol/schema/example.tsp
printf 'prompt\n' >internal/prompts/example.md
land 'Scripts and generated inputs'
expect_checks sync check-types check-prompts test-scripts land --group prompts "${selected_tests[@]}" sync

printf '\n' >>README.md
land 'Documentation change'
expect_checks sync sync

touch "$LAND_MOVE_MAIN"
printf '\n' >>internal/example/example.go
land 'Go change while main moves'
expect_checks sync check-go 'GO_PACKAGES=./internal/example' "${go_choice[@]}" \
  check-go 'GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

printf '\n' >>internal/example/example.go
LAND_FLAGS=--baseline land 'Go change landed on the baseline'
expect_checks check-go 'GO_PACKAGES=./internal/example' landing-tests go prompts
expect_output 'running the baseline landing tests (--baseline)'

printf '\n' >>internal/example/example.go
land 'Go change when choosing fails' LAND_SELECT_EXIT=1
expect_checks sync check-go 'GO_PACKAGES=./internal/example' land --group go --group prompts landing-tests go prompts sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

printf '\n' >>internal/example/example.go
land 'Go change when sync fails' LAND_SYNC_EXIT=1
expect_checks sync check-go 'GO_PACKAGES=./internal/example' "${go_choice[@]}" sync
expect_output 'choosing landing tests from the history already synced'
expect_output "this landing's test receipts upload at the next sync"

printf '\n' >>internal/example/example.go
land 'Go change when choosing writes nothing' LAND_SELECTED=
expect_checks sync check-go 'GO_PACKAGES=./internal/example' land --group go --group prompts landing-tests go prompts sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

refuses() {
  local before
  printf '\n' >>app/src/example.ts
  git add -A
  git commit -qm "$1"
  before="$(git --git-dir="$work/remote" rev-parse main)"
  if env "$2" bash "$root/scripts/land.sh" >"$work/failure" 2>&1; then
    echo "landing pushed despite $1" >&2
    exit 1
  fi
  [ "$before" = "$(git --git-dir="$work/remote" rev-parse main)" ]
  git reset -q --hard origin/main
}

refuses 'a failed app check' LAND_CHECK_EXIT=1
refuses 'a failed landing test' LAND_TESTS_EXIT=1

echo 'landing fast gate: OK'
