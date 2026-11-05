#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-test.XXXXXX")"
release_held_checks() {
  [ -p "$work/hold/release" ] || return 0
  exec 4<>"$work/hold/release"
  echo release >&4
  exec 4>&-
}
trap 'release_held_checks; rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/repo"
cat >"$work/bin/make" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "${2:-}" != compile-go ] || exit 0
printf '%s\n' "$*" >>"$LAND_CHECK_LOG"
echo "make ran: $*"
if [ -e "$LAND_MOVE_MAIN" ]; then
  rm "$LAND_MOVE_MAIN"
  git -C "$LAND_OTHER" pull -q origin main
  git -C "$LAND_OTHER" commit -q --allow-empty -m 'Landed during the gate'
  git -C "$LAND_OTHER" push -q origin HEAD:main
fi
if [ -n "${LAND_HOLD:-}" ]; then
  echo started >"$LAND_HOLD/started"
  read -r _ <"$LAND_HOLD/release"
fi
exit "${LAND_CHECK_EXIT:-0}"
EOF
chmod +x "$work/bin/make"
cat >"$work/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "$1 $2" = "tool test-history" ] || { echo "unexpected go $*" >&2; exit 2; }
case "$3" in
  sync)
    printf 'sync\n' >>"$LAND_CHECK_LOG"
    exit "${LAND_SYNC_EXIT:-0}"
    ;;
  land)
    printf '%s\n' "land ${*:6}" >>"$LAND_CHECK_LOG"
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
selected_units="--unit go ./internal/example TestExample --unit vitest src/example.test.ts"
go_choice=('land --group go --group prompts' "landing-tests --no-go-vet $selected_units")
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
printf '%s\n' "landing-tests $*" >>"$LAND_CHECK_LOG"
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

go_checks_last() {
  awk '/^check-go / { last = last $0 "\n"; next } { print } END { printf "%s", last }' "$1"
}

expect_checks() {
  printf '%s\n' "$@" >"$work/expected"
  diff -u <(go_checks_last "$work/expected") <(go_checks_last "$LAND_CHECK_LOG")
}

printf '\n' >>internal/example/example.go
land 'Go change'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

git rm -q internal/example/other.go internal/removed/removed.go
land 'Go deletions'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

printf '\n' >>go.mod
land 'Module change'
expect_checks 'check-go GO_PACKAGES=./...' "${go_choice[@]}" sync

mkdir -p app/src
printf 'export {};\n' >app/src/example.ts
land 'App change'
expect_checks 'check-frontend check-mobile' 'land --group app' "landing-tests $selected_units" sync

mkdir -p mobile/src
printf 'export {};\n' >mobile/src/example.ts
land 'Mobile change'
expect_checks check-mobile sync

mkdir -p scripts internal/protocol/schema internal/prompts
printf 'true\n' >scripts/example.sh
printf 'schema\n' >internal/protocol/schema/example.tsp
printf 'prompt\n' >internal/prompts/example.md
land 'Scripts and generated inputs'
expect_checks 'check-types check-prompts' 'land --group prompts' "landing-tests $selected_units" sync
expect_output 'the checkout has no script tests'

printf 'gateway\n' >internal/protocol/schema/gateway.tsp
land 'Gateway API change'
expect_checks 'check-types check-gateway-compat' sync

mkdir -p scripts/lib
printf 'true\n' >scripts/lib/shared.sh
printf 'bash scripts/example.sh\nsource scripts/lib/shared.sh\n' >scripts/example_test.sh
printf 'source scripts/lib/shared.sh\n' >scripts/other_test.sh
printf 'true\n' >scripts/unrelated_test.sh
land 'Script tests arrive'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh scripts/other_test.sh scripts/unrelated_test.sh' sync

printf '\n' >>scripts/example.sh
land 'Script change'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh' sync
expect_output 'script test scripts/example_test.sh, for scripts/example.sh'

printf '\n' >>scripts/lib/shared.sh
land 'Shared script change'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh scripts/other_test.sh' sync

mkdir -p .github/workflows fixtures
printf 'on: push\n' >.github/workflows/example.yml
printf 'one\n' >fixtures/one.txt
printf 'grep -q push "$root/.github/workflows/example.yml"\ncp -R "$root/fixtures" "$work"\n' >scripts/reads_test.sh
land 'Script test that reads checkout files arrives'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/reads_test.sh' sync

printf '\n' >>scripts/example.sh
printf '\n' >>.github/workflows/example.yml
land 'Script and workflow change'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh scripts/reads_test.sh' sync
expect_output 'script test scripts/reads_test.sh, for .github/workflows/example.yml'

printf 'two\n' >fixtures/two.txt
land 'Change inside a directory a script test reads'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/reads_test.sh' sync

printf 'source scripts/lib/deep.sh\n' >scripts/chain.sh
printf 'true\n' >scripts/lib/deep.sh
printf 'bash scripts/chain.sh\n' >scripts/chain_test.sh
land 'Chained script test arrives'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/chain_test.sh' sync

printf '\n' >>scripts/lib/deep.sh
land 'Change to a script a tested script sources'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/chain_test.sh' sync
expect_output 'script test scripts/chain_test.sh, for scripts/lib/deep.sh'

printf 'true\n' >scripts/lonely.sh
land 'Script no test names'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/chain_test.sh scripts/example_test.sh scripts/other_test.sh scripts/reads_test.sh scripts/unrelated_test.sh' sync
expect_output 'no script test names scripts/lonely.sh; running every script test'

printf '\n' >>README.md
land 'Documentation change'
expect_checks sync

touch "$LAND_MOVE_MAIN"
printf '\n' >>internal/example/example.go
land 'Go change while main moves'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" \
  'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

printf '\n' >>internal/example/example.go
LAND_FLAGS=--baseline land 'Go change landed on the baseline'
expect_checks 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts'
expect_output 'running the baseline landing tests (--baseline)'

printf '\n' >>internal/example/example.go
land 'Go change when choosing fails' LAND_SELECT_EXIT=1
expect_checks 'check-go GO_PACKAGES=./internal/example' 'land --group go --group prompts' 'landing-tests --no-go-vet go prompts' sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

printf '\n' >>internal/example/example.go
land 'Go change when sync fails' LAND_SYNC_EXIT=1
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync
expect_output "this landing's test receipts upload at the next sync"

printf '\n' >>internal/example/example.go
land 'Go change when choosing writes nothing' LAND_SELECTED=
expect_checks 'check-go GO_PACKAGES=./internal/example' 'land --group go --group prompts' 'landing-tests --no-go-vet go prompts' sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

refuses() {
  local before changing="${3:-app/src/example.ts}"
  printf '\n' >>"$changing"
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
refuses 'a failed Go check' LAND_CHECK_EXIT=1 internal/example/example.go
grep -qF 'make ran: check-go' "$work/failure" || { echo 'the failed Go check left no output' >&2; cat "$work/failure" >&2; exit 1; }
refuses 'a failed landing test beside passing Go checks' LAND_TESTS_EXIT=1 internal/example/example.go
grep -qF 'make ran: check-go' "$work/failure" || { echo 'the Go checks left no output' >&2; cat "$work/failure" >&2; exit 1; }

hold="$work/hold" second="$work/second"
mkdir "$hold"
mkfifo "$hold/started" "$hold/release" "$work/second-output"
git worktree add -q -b second "$second" origin/main

hold_queue() {
  printf '\n' >>app/src/example.ts
  git add -A
  git commit -qm "$1"
  LAND_HOLD="$hold" bash "$root/scripts/land.sh" >"$work/holder-output" 2>&1 &
  holder=$!
  read -r _ <"$hold/started"
}

land_second_in_background() {
  git -C "$second" pull -q --rebase origin main
  printf '%s\n' "$1" >"$second/$1.md"
  git -C "$second" add -A
  git -C "$second" commit -qm "$1"
  (cd "$second" && exec bash "$root/scripts/land.sh") >"$work/second-output" 2>&1 &
  waiter=$!
  exec 3<"$work/second-output"
  read -r first_line <&3
}

finish_second() {
  { printf '%s\n' "$first_line"; cat <&3; } >"$work/output"
  exec 3<&-
  wait "$waiter" || { cat "$work/output" >&2; exit 1; }
  [ "$(git -C "$second" rev-parse HEAD)" = "$(git --git-dir="$work/remote" rev-parse main)" ]
}

hold_queue 'App change that holds the land queue'
land_second_in_background waits
case "$first_line" in
  "land: waiting for the land queue: held by pid $holder (repo) since "*) ;;
  *) echo "the second land did not wait for the queue: $first_line" >&2; exit 1 ;;
esac
echo release >"$hold/release"
wait "$holder"
finish_second
expect_output 'land: took the land queue'
if grep -qF 'main moved' "$work/output"; then cat "$work/output" >&2; exit 1; fi

hold_queue 'App change whose land is killed'
kill -KILL "$holder"
wait "$holder" 2>/dev/null || true
land_second_in_background follows-killed
echo release >"$hold/release"
case "$first_line" in
  "land: main is at "*) ;;
  *) echo "a killed land kept the queue: $first_line" >&2; exit 1 ;;
esac
finish_second
git reset -q --hard origin/main

mkdir -p "$work/tests-repo/scripts"
cd "$work/tests-repo"
git init -q
cp "$root/scripts/landing-tests" "$root/scripts/landing-tests.sh" scripts/
cat >scripts/test-go.sh <<'EOF'
#!/usr/bin/env bash
if [ "$1" = --provide-test-binaries ]; then
  printf 'fake-attn\nfake-agent\n' >"$2"
  exit 0
fi
printf '%s\n' "$*" >>"$LAND_CHECK_LOG"
while [ "$#" -gt 0 ]; do
  [ "$1" != --receipt-out ] || : >"$2"
  shift
done
EOF
chmod +x scripts/test-go.sh
: >"$LAND_CHECK_LOG"
scripts/landing-tests.sh --unit 'go ./internal/example TestExample' --unit 'go ./cmd/attn TestOne' --unit 'go ./cmd/attn TestTwo' >/dev/null
if grep -F -- ./internal/example "$LAND_CHECK_LOG" | grep -qF -- '-vet=off'; then
  echo 'landing tests run by hand left out the vet of go test' >&2
  exit 1
fi
[ "$(grep -F -- ./cmd/attn "$LAND_CHECK_LOG" | grep -cvF -- '-vet=off')" = 1 ] || { echo 'expected exactly one ./cmd/attn run to keep the vet of go test' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
: >"$LAND_CHECK_LOG"
scripts/landing-tests.sh --no-go-vet --unit 'go ./internal/example TestExample' >/dev/null
grep -qF -- '-vet=off' "$LAND_CHECK_LOG" || { echo '--no-go-vet kept the vet of go test' >&2; exit 1; }

echo 'landing fast gate: OK'
