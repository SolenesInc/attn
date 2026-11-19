#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-test.XXXXXX")"
release_held_checks() {
  local fifo
  for fifo in "$work/hold/release" "$work/soak-release"; do
    [ -p "$fifo" ] || continue
    exec 4<>"$fifo"
    echo release >&4
    exec 4>&-
  done
}
trap 'release_held_checks; rm -rf "$work"' EXIT

mkdir -p "$work/bin" "$work/repo"
cat >"$work/bin/make" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "${2:-}" != compile-go ] || exit "${LAND_COMPILE_EXIT:-0}"
printf '%s\n' "$*" >>"$LAND_CHECK_LOG"
echo "make ran: $*"
if [ -e "$LAND_MOVE_MAIN" ]; then
  rm "$LAND_MOVE_MAIN"
  git -C "$LAND_OTHER" pull -q origin main
  git -C "$LAND_OTHER" commit -q --allow-empty -m 'Landed during the gate'
  git -C "$LAND_OTHER" push -q origin HEAD:main
fi
if [ -p "${LAND_GATE_AWAITS_SOAK:-}" ]; then
  read -r _ <"$LAND_GATE_AWAITS_SOAK"
  rm "$LAND_GATE_AWAITS_SOAK"
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
  capture)
    printf 'capture in %s: %s\n' "$(basename "$PWD")" "${*:4}" >>"$LAND_CHECK_LOG"
    while [ "$#" -gt 0 ]; do
      [ "$1" != --receipt-out ] || : >"$2"
      shift
    done
    ;;
  summary) echo 'PASS cargo example --lib (0.0s)' ;;
  land)
    printf '%s\n' "land ${*:6}" >>"$LAND_CHECK_LOG"
    [ "${LAND_SELECT_EXIT:-0}" -eq 0 ] || exit "$LAND_SELECT_EXIT"
    [ "$4" = --units-out ] || exit 2
    printf '%s' "${LAND_SELECTED-$selected}" >"$5"
    ;;
esac
EOF
chmod +x "$work/bin/go"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "workflow run")
    printf 'soak %s\n' "$*" >>"$LAND_CHECK_LOG"
    git --git-dir="$LAND_REMOTE" for-each-ref --format='%(refname)' refs/heads/land-scenarios >>"$LAND_SOAK_REFS"
    ;;
  "run list") echo 7 ;;
  "run view") echo https://example.invalid/runs/7 ;;
  "run watch")
    if [ -n "${LAND_SOAK_HOLD:-}" ]; then
      echo watching >"$LAND_SOAK_HOLD/watching"
      read -r _ <"$LAND_SOAK_HOLD/release"
    fi
    exit "${LAND_SOAK_EXIT:-0}"
    ;;
  "run cancel") printf 'soak %s\n' "$*" >>"$LAND_CHECK_LOG" ;;
  *) echo "unexpected gh $*" >&2; exit 2 ;;
esac
EOF
chmod +x "$work/bin/gh"
cat >"$work/bin/pnpm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
args="$*"
case "$args" in
  *"real-app:linux status --provider ssh --target "*) exit "${LAND_RUNNER_REACH_EXIT:-0}" ;;
  *"real-app:linux soak "*)
    printf 'runner %s\n' "${args#*real-app:linux }" >>"$LAND_CHECK_LOG"
    case "$args" in
      *"--target ${LAND_RUNNER_BUSY:-none} "*"--turn-limit 0 "*|*"--target ${LAND_RUNNER_ALSO_BUSY:-none} "*"--turn-limit 0 "*)
        echo "Gave up after 0s waiting for the guest checkout land" >&2
        exit 75 ;;
    esac
    if [ -n "${LAND_RUNNER_GAVE_UP:-}" ]; then
      echo "Gave up after ${LAND_RUNNER_GAVE_UP}s waiting for the guest checkout land" >&2
      exit 75
    fi
    echo "Waited ${LAND_RUNNER_WAITED:-0}s for the guest checkout land" >&2
    case "$args" in
      *--native-wire*)
        if [ -n "${LAND_RUNNER_WIRE_FAIL:-}" ]; then
          echo "test wire::$LAND_RUNNER_WIRE_FAIL ... FAILED"
          echo "Native wire tests failed in 4s"
          exit 1
        fi
        echo "Native wire tests passed in 4s"
        ;;
    esac
    scenarios=""
    case "$args" in *"--scenario "*) scenarios="${args#*--scenario }"; scenarios="${scenarios%% *}" ;; esac
    if [ -n "${LAND_RUNNER_HOLD:-}" ]; then
      echo "Scenario ${scenarios%%,*} passed in 2s"
      bash -c 'trap "echo \"runner child stopped\" >>\"\$LAND_CHECK_LOG\"; exit 143" TERM
        echo running >"$LAND_RUNNER_HOLD/watching"
        read -r _ <>"$LAND_RUNNER_HOLD/release"' &
      wait
    fi
    status=0
    for scenario in ${scenarios//,/ }; do
      [ "$scenario" != "${LAND_RUNNER_CRASH_AT:-}" ] || exit 1
      if [ "$scenario" = "${LAND_RUNNER_FAIL:-}" ]; then echo "Scenario $scenario failed in 3s"; status=1
      else echo "Scenario $scenario passed in 2s"; fi
    done
    exit "$status"
    ;;
  *) echo "unexpected pnpm $*" >&2; exit 2 ;;
esac
EOF
chmod +x "$work/bin/pnpm"
cat >"$work/bin/uname" <<'EOF'
#!/usr/bin/env bash
[ "$*" = -s ] || { echo "unexpected uname $*" >&2; exit 2; }
echo "${LAND_HOST_OS:-Linux}"
EOF
chmod +x "$work/bin/uname"
cat >"$work/bin/attn" <<'EOF'
#!/usr/bin/env bash
[ "$*" = "seed show s-fix123" ]
EOF
chmod +x "$work/bin/attn"
unset ATTN_FREEZE_EXCEPTION
export PATH="$work/bin:$PATH"
export LAND_CLEAN_LOG="$work/cleans"
export LAND_CHECK_LOG="$work/checks" LAND_MOVE_MAIN="$work/move-main" LAND_OTHER="$work/other"
export LAND_REMOTE="$work/remote" LAND_SOAK_REFS="$work/soak-refs" LAND_REJECT_MAIN="$work/reject-main"
export selected=$'go ./internal/example TestExample\nvitest src/example.test.ts'
selected_units="--unit go ./internal/example TestExample --unit vitest src/example.test.ts"
go_choice=('land --group go --group prompts' "landing-tests --no-go-vet $selected_units")
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git init -q --bare "$work/remote"
cat >"$work/remote/hooks/pre-receive" <<'EOF'
#!/usr/bin/env bash
while read -r _ _ ref; do
  if [ "$ref" = refs/heads/main ] && [ -e "$LAND_REJECT_MAIN" ]; then
    echo 'main rejects pushes for this test' >&2
    exit 1
  fi
done
EOF
chmod +x "$work/remote/hooks/pre-receive"
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
cp "$root/scripts/check-cloud-relay-privacy" scripts/check-cloud-relay-privacy
cp "$root/go-cgo-cflags" go-cgo-cflags
cat >scripts/landing-tests.sh <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "landing-tests $*" >>"$LAND_CHECK_LOG"
exit "${LAND_TESTS_EXIT:-0}"
EOF
chmod +x scripts/landing-tests.sh
cat >scripts/native-build-clean <<'EOF'
#!/usr/bin/env bash
echo cleaned >>"$LAND_CLEAN_LOG"
echo "native-build-clean: removed an old build"
EOF
chmod +x scripts/native-build-clean
cp "$root/scripts/land-linux.sh" scripts/land-linux.sh
mkdir -p app/scripts/real-app-harness
cp "$root"/app/scripts/real-app-harness/{landing-scenarios,scenarioCatalog,matrixDigest}.mjs app/scripts/real-app-harness/
cp "$root/app/package.json" app/package.json
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

printf 'kept\n' >kept.txt
: >"$LAND_CLEAN_LOG"
land 'Add the kept file'
[ "$(cat "$LAND_CLEAN_LOG")" = cleaned ] || { echo 'a landing did not clean old native build output after its push' >&2; exit 1; }
expect_output 'native-build-clean: removed an old build'
if grep -qF 'WARNING' "$work/output"; then
  echo 'a landing that undoes nothing warned' >&2
  cat "$work/output" >&2
  exit 1
fi
kept_commit="$(git log -1 --format='%h Add the kept file')"
git rm -q kept.txt
land 'Drop the kept file'
expect_output 'WARNING: this landing undoes earlier commits on main'
expect_output "$kept_commit"
[ "$(tail -1 "$work/output" | cut -c1-5)" = 'land:' ] || { echo 'the warning is not the end of the output' >&2; cat "$work/output" >&2; exit 1; }
seq 1 20 >counted.txt
land 'Add the counted file'
{ seq 1 3; echo kept; seq 4 20; } >counted.txt
land 'Keep a line'
seq 1 20 >counted.txt
land 'Drop the line'
expect_output 'Keep a line'
{ seq 1 20; echo later; } >counted.txt
land 'Add a later line'
if grep -qF 'WARNING' "$work/output"; then
  echo 'a landing warned about a commit an earlier landing undid' >&2
  cat "$work/output" >&2
  exit 1
fi

printf '\n' >>internal/example/example.go
land 'Go change'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

git rm -q internal/example/other.go internal/removed/removed.go
land 'Go deletions'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

printf '\n' >>go.mod
land 'Module change'
expect_checks "${go_choice[0]}" 'check-types' 'check-go GO_PACKAGES=./...' "${go_choice[1]}" sync

mkdir -p app/src
printf 'export {};\n' >app/src/example.ts
git add -A
git commit -qm 'Change the frozen app'
if bash "$root/scripts/land.sh" >"$work/output" 2>&1; then echo 'a change to the frozen app landed without an exception' >&2; exit 1; fi
expect_output 'the Tauri app is frozen'
expect_output 'app/src/example.ts'
expect_output 'ATTN_FREEZE_EXCEPTION=<seed id> scripts/land.sh'
if ATTN_FREEZE_EXCEPTION=s-nope00 bash "$root/scripts/land.sh" >"$work/output" 2>&1; then echo 'an exception naming no seed let the frozen app change land' >&2; exit 1; fi
expect_output 'ATTN_FREEZE_EXCEPTION=s-nope00 names no seed'
git reset -q --soft HEAD^
export ATTN_FREEZE_EXCEPTION=s-fix123
land 'App change'
expect_checks 'land --group app' check-frontend "landing-tests $selected_units" sync
expect_output 'changing the frozen Tauri app under the exception for seed s-fix123'
unset ATTN_FREEZE_EXCEPTION

mkdir -p app/src/types
printf 'export {};\n' >app/src/types/generated.ts
land 'Regenerated protocol types reach the frozen app'

mkdir -p native/crates/demo/src
printf '[workspace]\n' >native/Cargo.toml
printf '[package]\n' >native/crates/demo/Cargo.toml
printf '\n' >native/crates/demo/src/lib.rs
land 'Native workspace arrives'
expect_checks 'land --group native' 'check-native NATIVE_CLIPPY=--workspace' "landing-tests $selected_units" sync

printf '\n' >>native/crates/demo/src/lib.rs
land 'Native crate change'
expect_checks 'land --group native' 'check-native NATIVE_CLIPPY=-p demo' "landing-tests $selected_units" sync
export ATTN_FREEZE_EXCEPTION=s-fix123

printf '\n' >>app/src/example.ts
land 'App change while a Vitest file is red on main' "LAND_SELECTED=$(printf 'vitest .\nred vitest src/red.test.ts')"
expect_checks 'land --group app' check-frontend 'landing-tests --unit vitest . --red vitest src/red.test.ts' sync

mkdir -p mobile/src
printf 'export {};\n' >mobile/src/example.ts
land 'Mobile change'
expect_checks check-mobile sync

mkdir -p scripts internal/protocol/schema internal/prompts
printf 'true\n' >scripts/example.sh
printf 'schema\n' >internal/protocol/schema/example.tsp
printf 'prompt\n' >internal/prompts/example.md
land 'Scripts and generated inputs'
expect_checks 'land --group prompts --group native' 'check-types check-prompts check-native NATIVE_CLIPPY=' "landing-tests $selected_units" sync
expect_output 'the checkout has no script tests'

mkdir -p internal/setting
printf 'setting\n' >internal/setting/example.txt
land 'Settings declaration change'
expect_checks 'check-types' sync

printf 'Home Gateway\n' >internal/protocol/schema/home_gateway.tsp
land 'HomeGateway API change'
expect_checks 'land --group old-phones --group native' 'check-types check-home-gateway-compat check-native NATIVE_CLIPPY=' "landing-tests $selected_units" sync

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

no_land_scenario_refs() {
  [ -z "$(git --git-dir="$work/remote" for-each-ref refs/heads/land-scenarios)" ] || { echo 'the scenario run left its ref on the remote' >&2; exit 1; }
}

mkdir -p app/scripts/real-app-harness
printf '// changed\n' >app/scripts/real-app-harness/scenario-shelf-moves.mjs
: >"$LAND_SOAK_REFS"
land 'Change a scenario, named through a package script'
grep -qF 'soak workflow run acceptance-soak.yml' "$LAND_CHECK_LOG" || { echo 'a changed scenario did not run before the push' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF -- '-f scenarios=shelf-moves -f repeat=1' "$LAND_CHECK_LOG" || { echo 'the soak did not name the changed scenario' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF 'check-frontend' "$LAND_CHECK_LOG" || { echo 'the gate did not run beside the scenarios' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF refs/heads/land-scenarios/ "$LAND_SOAK_REFS" || { echo 'the soak ran before its ref reached the remote' >&2; exit 1; }
no_land_scenario_refs
expect_output 'passed on CI'

printf '// changed\n' >app/scripts/real-app-harness/scenario-focusless-native.mjs
land 'Change a macOS-only scenario'
if grep -qF 'soak' "$LAND_CHECK_LOG"; then echo 'a macOS-only scenario ran on Linux' >&2; exit 1; fi
expect_output 'scenario focusless-native changed and does not run on Linux'

mkdir -p .agents/skills/drive-attn/features
printf '## Tests\n\n- Wire: `internal/example/example_wire_test.go`.\n- Scenario: `shelf-layout`, `focusless-native`, `shelf-peek`.\n' >.agents/skills/drive-attn/features/example.md
land 'Reword a feature file without changing the product'
if grep -qF 'soak' "$LAND_CHECK_LOG"; then echo 'a feature file alone ran its scenarios' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; fi

printf '\n' >>internal/example/example.go
land 'Change the product without changing a feature file'
if grep -qF 'soak' "$LAND_CHECK_LOG"; then echo 'a product change alone ran a scenario' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; fi

printf '\n' >>internal/example/example.go
printf '\n' >>.agents/skills/drive-attn/features/example.md
land 'Change the product and the feature file that describes it'
grep -qF -- '-f scenarios=shelf-layout,shelf-peek -f repeat=1' "$LAND_CHECK_LOG" || { echo 'the soak did not name the feature file scenarios that run on Linux' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
no_land_scenario_refs

touch "$LAND_MOVE_MAIN"
printf '\n' >>internal/example/example.go
land 'Go change while main moves'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync
expect_output 'main moved after the checks ran on'

printf '\n' >>internal/example/example.go
LAND_FLAGS=--baseline land 'Go change landed on the baseline'
expect_checks 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts'
expect_output 'running the baseline landing tests (--baseline)'

printf '\n' >>internal/example/example.go
land 'Go change when choosing fails' LAND_SELECT_EXIT=1
expect_checks 'land --group go --group prompts' 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts' sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

printf '\n' >>internal/example/example.go
land 'Go change when sync fails' LAND_SYNC_EXIT=1
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync
expect_output "this landing's test receipts upload at the next sync"

printf '\n' >>internal/example/example.go
land 'Go change when choosing writes nothing' LAND_SELECTED=
expect_checks 'land --group go --group prompts' 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts' sync
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

printf '[user]\n\temail = global@example.invalid\n' >"$work/global-identity"
refuses 'a commit whose author is not the global identity' GIT_CONFIG_GLOBAL="$work/global-identity"
grep -qF 'landing@example.invalid' "$work/failure" && grep -qF 'global@example.invalid' "$work/failure" || { echo 'the refusal did not name both identities' >&2; cat "$work/failure" >&2; exit 1; }
printf '[user]\n\temail = landing@example.invalid\n' >"$work/matching-identity"
printf '\n' >>app/src/example.ts
land 'A commit by the global identity' GIT_CONFIG_GLOBAL="$work/matching-identity"

refuses 'a failed app check' LAND_CHECK_EXIT=1
refuses 'a failed landing test' LAND_TESTS_EXIT=1
refuses 'a failed Go check' LAND_CHECK_EXIT=1 internal/example/example.go
grep -qF 'make ran: check-go' "$work/failure" || { echo 'the failed Go check left no output' >&2; cat "$work/failure" >&2; exit 1; }
refuses 'a failed landing test beside passing Go checks' LAND_TESTS_EXIT=1 internal/example/example.go
grep -qF 'make ran: check-go' "$work/failure" || { echo 'the Go checks left no output' >&2; cat "$work/failure" >&2; exit 1; }
refuses 'a changed scenario that fails on CI' LAND_SOAK_EXIT=1 app/scripts/real-app-harness/scenario-shelf-moves.mjs
grep -qF 'gh run view 7 --log-failed' "$work/failure" || { echo 'the failed scenario did not say how to read it' >&2; cat "$work/failure" >&2; exit 1; }
no_land_scenario_refs
refuses 'a failed Go compile' LAND_COMPILE_EXIT=1 internal/example/example.go

records="$(git rev-parse --git-common-dir)/attn-land-scenarios"
record_for_landing() {
  printf '%s/%s' "$records" "$(git diff origin/main...HEAD | git patch-id --verbatim | cut -d' ' -f1)"
}

mkdir "$work/soak-hold"
mkfifo "$work/soak-hold/watching" "$work/soak-release"
ln -s "$work/soak-release" "$work/soak-hold/release"
printf '// gate fails\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
git add -A
git commit -qm 'Scenario change whose gate fails while its scenario runs'
: >"$LAND_CHECK_LOG"
before="$(git --git-dir="$work/remote" rev-parse main)"
if env LAND_SOAK_HOLD="$work/soak-hold" LAND_GATE_AWAITS_SOAK="$work/soak-hold/watching" LAND_CHECK_EXIT=1 bash "$root/scripts/land.sh" >"$work/output" 2>&1; then
  echo 'a landing whose gate failed pushed' >&2
  exit 1
fi
[ "$before" = "$(git --git-dir="$work/remote" rev-parse main)" ]
grep -qxF 'soak run cancel 7' "$LAND_CHECK_LOG" || { echo 'a failed gate did not cancel the running scenarios' >&2; cat "$LAND_CHECK_LOG" "$work/output" >&2; exit 1; }
expect_output 'stopped the scenario step'
no_land_scenario_refs
[ ! -e "$(record_for_landing)" ] || { echo 'a stopped scenario step recorded a pass' >&2; exit 1; }

touch "$LAND_REJECT_MAIN"
: >"$LAND_CHECK_LOG"
if bash "$root/scripts/land.sh" >"$work/output" 2>&1; then echo 'main accepted a push it rejects' >&2; exit 1; fi
grep -qF 'soak workflow run' "$LAND_CHECK_LOG" || { echo 'the scenario did not run' >&2; cat "$work/output" >&2; exit 1; }
grep -qxF 'shelf-moves github https://example.invalid/runs/7' "$(record_for_landing)" || { echo 'a passed scenario left no pass record' >&2; exit 1; }
git -C "$LAND_OTHER" pull -q origin main
printf 'moved\n' >"$LAND_OTHER/moved.md"
git -C "$LAND_OTHER" add -A
git -C "$LAND_OTHER" commit -qm 'Main moves before the landing runs again'
rm "$LAND_REJECT_MAIN"
git -C "$LAND_OTHER" push -q origin HEAD:main
touch "$LAND_REJECT_MAIN"
: >"$LAND_CHECK_LOG"
if bash "$root/scripts/land.sh" >"$work/output" 2>&1; then echo 'main accepted a push it rejects' >&2; exit 1; fi
if grep -qF soak "$LAND_CHECK_LOG"; then echo 'an unchanged diff ran its scenario again after a rebase' >&2; cat "$work/output" >&2; exit 1; fi
expect_output 'scenario shelf-moves passed on this diff already: https://example.invalid/runs/7'
grep -qF 'check-frontend' "$LAND_CHECK_LOG" || { echo 'a landing with recorded scenarios skipped its gate' >&2; exit 1; }
rm "$LAND_REJECT_MAIN"
printf 'changed diff\n' >>README.md
git add -A
git commit -q --amend --no-edit
: >"$LAND_CHECK_LOG"
bash "$root/scripts/land.sh" >"$work/output" 2>&1 || { cat "$work/output" >&2; exit 1; }
[ "$(git rev-parse HEAD)" = "$(git --git-dir="$work/remote" rev-parse main)" ]
grep -qF 'soak workflow run' "$LAND_CHECK_LOG" || { echo 'a changed diff did not run its scenario again' >&2; cat "$work/output" >&2; exit 1; }

recently_failed="$selected"$'\nnative shelf-peek\nnative focusless-native\nnative retired-scenario'
expect_recent_scenarios_ran() {
  grep -qxF "${go_choice[1]}" "$LAND_CHECK_LOG" || { echo 'the landing tests were not handed exactly the units that are not scenarios' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
  expect_output 'scenario focusless-native failed on main recently and does not run on Linux'
  expect_output 'scenario retired-scenario failed on main recently and is no longer in the scenario catalog'
}
printf '\n' >>internal/example/example.go
land 'Go change while a scenario that failed on main has since passed there' LAND_SELECTED="$recently_failed"
grep -qF -- '-f scenarios=shelf-peek -f repeat=1' "$LAND_CHECK_LOG" || { echo 'the scenario step did not run the scenario the landing tests chose' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'land: scenarios shelf-peek passed on CI'"'"'s Linux'
expect_recent_scenarios_ran
no_land_scenario_refs

git config attn.landRunner ssh:runner.invalid
printf '// on the landing runner\n' >>internal/example/example.go
land 'Go change on the landing runner while a scenario that failed on main has since passed there' LAND_SELECTED="$recently_failed"
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 --scenario shelf-peek --out ' "$LAND_CHECK_LOG" || { echo 'the landing runner did not run the scenario the landing tests chose' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario shelf-peek passed on runner.invalid in 2s'
expect_recent_scenarios_ran

printf '\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
land 'Change a scenario on the landing runner' LAND_RUNNER_WAITED=7
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 --scenario shelf-moves --out ' "$LAND_CHECK_LOG" || { echo 'the scenario did not run on the landing runner' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
if grep -qF 'workflow run' "$LAND_CHECK_LOG"; then echo 'a scenario that passed on the landing runner ran on CI' >&2; exit 1; fi
expect_output 'scenario shelf-moves passed on runner.invalid in 2s'
expect_output 's, 7s of it waiting for other landings'
grep -rqE '^shelf-moves ssh:runner.invalid /.*/runner.log$' "$records" || { echo 'a scenario that passed on the landing runner left no pass record' >&2; exit 1; }

printf '\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
printf '// busy runner\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
land 'Change a scenario while the landing runner stays busy past the turn limit' LAND_RUNNER_GAVE_UP=655
grep -qF -- '-f scenarios=shelf-moves -f repeat=1' "$LAND_CHECK_LOG" || { echo 'a landing that gave up its wait for the landing runner did not run on CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output "waited 655s for a turn on the landing runner runner.invalid, past its 654s limit, so the scenarios run on CI's Linux instead"
grep -qE '^shelf-moves github ' "$(ls -t "$records"/* | head -1)" || { echo 'a scenario that passed on CI after the wait left no pass record' >&2; exit 1; }

printf '\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
land 'Change a scenario while the landing runner is down' LAND_RUNNER_REACH_EXIT=255
if grep -qF runner "$LAND_CHECK_LOG"; then echo 'a scenario ran on an unreachable landing runner' >&2; exit 1; fi
grep -qF -- '-f scenarios=shelf-moves -f repeat=1' "$LAND_CHECK_LOG" || { echo 'an unreachable landing runner did not fall back to CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'the landing runner runner.invalid cannot be reached'

git config --add attn.landRunner ssh:second.invalid
printf '// two runners\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
land 'Change a scenario while the first of two landing runners is busy' LAND_RUNNER_BUSY=runner.invalid
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 0 ' "$LAND_CHECK_LOG" || { echo 'a landing with two runners waited on the first one' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'the landing runner runner.invalid is busy with another landing'
expect_output 'scenario shelf-moves passed on second.invalid in 2s'
if grep -qF 'workflow run' "$LAND_CHECK_LOG"; then echo 'a scenario that passed on the second landing runner ran on CI' >&2; exit 1; fi
grep -rqE '^shelf-moves ssh:second.invalid ' "$records" || { echo 'a scenario that passed on the second landing runner left no pass record' >&2; exit 1; }

printf '// both busy\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
land 'Change a scenario while both landing runners are busy' LAND_RUNNER_BUSY=runner.invalid LAND_RUNNER_ALSO_BUSY=second.invalid
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 ' "$LAND_CHECK_LOG" || { echo 'a landing that found every runner busy did not wait for the first' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario shelf-moves passed on runner.invalid in 2s'
git config --unset-all attn.landRunner
git config attn.landRunner ssh:runner.invalid

printf '\n' >>internal/example/example.go
printf '\n' >>.agents/skills/drive-attn/features/example.md
land 'Change the product while a scenario fails on the landing runner' LAND_RUNNER_FAIL=shelf-peek
[ "$(grep -c '^runner soak ' "$LAND_CHECK_LOG")" = 1 ] && grep -qE -- '--scenario (shelf-layout,shelf-peek|shelf-peek,shelf-layout) ' "$LAND_CHECK_LOG" || { echo 'the landing runner did not run every scenario in one run' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF -- '-f scenarios=shelf-peek -f repeat=1' "$LAND_CHECK_LOG" || { echo 'only the scenario that failed on the landing runner should run on CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario shelf-peek failed on runner.invalid in 3s, so it runs again on CI'
grep -rqE '^shelf-layout ssh:runner.invalid ' "$records" || { echo 'a scenario that passed beside a failed one left no pass record' >&2; exit 1; }
no_land_scenario_refs

printf '// runner dies\n' >>internal/example/example.go
printf 'runner dies\n' >>.agents/skills/drive-attn/features/example.md
land 'Change the product while the landing runner dies before its last scenario' LAND_RUNNER_CRASH_AT=shelf-peek
grep -qF -- '-f scenarios=shelf-peek -f repeat=1' "$LAND_CHECK_LOG" || { echo 'a scenario the landing runner did not finish should run on CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario shelf-peek did not finish on runner.invalid, so it runs again on CI'
record="$(grep -rlE '^shelf-peek github ' "$records" | xargs ls -t | head -1)"
grep -qE '^shelf-layout ssh:runner.invalid ' "$record" || { echo 'the scenario the landing runner finished left no pass record' >&2; cat "$record" >&2; exit 1; }
if grep -qE '^shelf-peek ssh:' "$record"; then echo 'a scenario the landing runner did not finish was recorded as passed there' >&2; exit 1; fi

export LAND_RUNNER_FAIL=shelf-moves
refuses 'a scenario that fails on the landing runner and on CI' LAND_SOAK_EXIT=1 app/scripts/real-app-harness/scenario-shelf-moves.mjs
unset LAND_RUNNER_FAIL
grep -qF 'gh run view 7 --log-failed' "$work/failure" || { echo 'the scenario that failed on CI did not block the landing' >&2; cat "$work/failure" >&2; exit 1; }
no_land_scenario_refs

mkdir "$work/runner-hold"
mkfifo "$work/runner-hold/watching" "$work/runner-hold/release"
printf '// gate fails on the runner\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
printf '// gate fails on the runner\n' >>app/scripts/real-app-harness/scenario-shelf-peek.mjs
git add -A
git commit -qm 'Scenario change whose gate fails while it runs on the landing runner'
: >"$LAND_CHECK_LOG"
before="$(git --git-dir="$work/remote" rev-parse main)"
if env LAND_RUNNER_HOLD="$work/runner-hold" LAND_GATE_AWAITS_SOAK="$work/runner-hold/watching" LAND_CHECK_EXIT=1 bash "$root/scripts/land.sh" >"$work/output" 2>&1; then
  echo 'a landing whose gate failed pushed' >&2
  exit 1
fi
[ "$before" = "$(git --git-dir="$work/remote" rev-parse main)" ]
grep -qxF 'runner child stopped' "$LAND_CHECK_LOG" || { echo 'a failed gate did not stop the processes of the scenario on the landing runner' >&2; cat "$LAND_CHECK_LOG" "$work/output" >&2; exit 1; }
first="$(sed -n 's/^runner soak .*--scenario \([^,]*\),.*/\1/p' "$LAND_CHECK_LOG")"
[ -n "$first" ] || { echo 'the landing runner did not run both scenarios in one run' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
[ "$(cut -d' ' -f1 "$(record_for_landing)")" = "$first" ] || { echo 'a stopped landing runner must keep the pass it finished and record no other' >&2; cat "$(record_for_landing)" >&2; exit 1; }
git reset -q --hard origin/main

printf '// on Linux\n' >>native/crates/demo/src/lib.rs
land 'Native change from a Linux host' LAND_SELECTED='cargo .'
if grep -qF -- --native-wire "$LAND_CHECK_LOG"; then echo 'a native change landed from Linux ran the wire suite on the landing runner as well as in its landing tests' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; fi

printf '// from a Mac\n' >>native/crates/demo/src/lib.rs
land 'Native change from a Mac' LAND_HOST_OS=Darwin LAND_SELECTED='cargo .'
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 --out [^ ]+ --native-wire$' "$LAND_CHECK_LOG" || { echo 'a native change landed from a Mac did not run the wire suite on the landing runner' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'the native wire suite passed on runner.invalid in 4s'

export LAND_HOST_OS=Darwin
refuses 'a native change from a Mac whose wire suite fails on Linux' LAND_RUNNER_WIRE_FAIL=tiles::a_tile_fits native/crates/demo/src/lib.rs
unset LAND_HOST_OS
grep -qF 'the native wire suite failed on Linux, on runner.invalid in 4s, so nothing was pushed to main' "$work/failure" || { echo 'a wire suite that failed on Linux did not stop the push from a Mac' >&2; cat "$work/failure" >&2; exit 1; }
grep -qxF 'test wire::tiles::a_tile_fits ... FAILED' "$work/failure" || { echo 'the landing did not name the wire test that failed on Linux' >&2; cat "$work/failure" >&2; exit 1; }

printf '// beside a scenario\n' >>native/crates/demo/src/lib.rs
printf '// beside the wire suite\n' >>app/scripts/real-app-harness/scenario-shelf-moves.mjs
land 'Native and scenario change from a Mac' LAND_HOST_OS=Darwin LAND_SELECTED='cargo .'
[ "$(grep -c '^runner soak ' "$LAND_CHECK_LOG")" = 1 ] && grep -qE -- '--scenario shelf-moves --out [^ ]+ --native-wire$' "$LAND_CHECK_LOG" || { echo 'the wire suite and the scenario did not share one turn on the landing runner' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }

printf '// runner down\n' >>native/crates/demo/src/lib.rs
land 'Native change from a Mac while the landing runner is down' LAND_HOST_OS=Darwin LAND_RUNNER_REACH_EXIT=255 LAND_SELECTED='cargo .'
expect_output "no landing runner can be reached, so the native wire suite runs on Linux only in CI's Native job, after the push"

git config attn.landRunner runner.invalid
refuses 'a landing runner that names no runner' GIT_CONFIG_NOSYSTEM=1 app/scripts/real-app-harness/scenario-shelf-moves.mjs
grep -qF 'set it to github or ssh:<target>' "$work/failure" || { cat "$work/failure" >&2; exit 1; }
git config --unset attn.landRunner

hold="$work/hold" second="$work/second"
mkdir "$hold"
mkfifo "$hold/started" "$hold/release"
git worktree add -q -b second "$second" origin/main

printf '\n' >>app/src/example.ts
git add -A
git commit -qm 'App change whose checks run beside another land'
LAND_HOLD="$hold" bash "$root/scripts/land.sh" >"$work/holder-output" 2>&1 &
holder=$!
read -r _ <"$hold/started"
git -C "$second" pull -q --rebase origin main
printf 'beside\n' >"$second/beside.md"
git -C "$second" add -A
git -C "$second" commit -qm 'Land beside running checks'
(cd "$second" && exec bash "$root/scripts/land.sh") >"$work/output" 2>&1 || { cat "$work/output" >&2; exit 1; }
[ "$(git -C "$second" rev-parse HEAD)" = "$(git --git-dir="$work/remote" rev-parse main)" ] || { echo 'a land did not push while another land of the clone ran its checks' >&2; exit 1; }
echo release >"$hold/release"
wait "$holder" || { cat "$work/holder-output" >&2; exit 1; }
[ "$(git rev-parse HEAD)" = "$(git --git-dir="$work/remote" rev-parse main)" ]
[ "$(grep -c '^make ran:' "$work/holder-output")" = 1 ] || { echo 'a land ran its checks again after main moved' >&2; cat "$work/holder-output" >&2; exit 1; }
grep -qF 'main moved after the checks ran on' "$work/holder-output" || { cat "$work/holder-output" >&2; exit 1; }
git reset -q --hard origin/main

mkdir -p "$work/tests-repo/scripts"
cd "$work/tests-repo"
git init -q
cp "$root/scripts/landing-tests" "$root/scripts/landing-tests.sh" scripts/
cat >scripts/test-go.sh <<'EOF'
#!/usr/bin/env bash
if [ "$1" = --provide-test-binaries ]; then
  printf 'fake-attn\nfake-agent\nfake-host\n' >"$2"
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
scripts/landing-tests.sh --unit 'go ./internal/example TestExample' --unit 'go ./cmd/attnd TestOne' --unit 'go ./cmd/attnd TestTwo' >/dev/null
if grep -F -- ./internal/example "$LAND_CHECK_LOG" | grep -qF -- '-vet=off'; then
  echo 'landing tests run by hand left out the vet of go test' >&2
  exit 1
fi
[ "$(grep -F -- ./cmd/attnd "$LAND_CHECK_LOG" | grep -cvF -- '-vet=off')" = 1 ] || { echo 'expected exactly one ./cmd/attnd run to keep the vet of go test' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
: >"$LAND_CHECK_LOG"
scripts/landing-tests.sh --no-go-vet --unit 'go ./internal/example TestExample' >/dev/null
grep -qF -- '-vet=off' "$LAND_CHECK_LOG" || { echo '--no-go-vet kept the vet of go test' >&2; exit 1; }

mkdir -p native
: >"$LAND_CHECK_LOG"
scripts/landing-tests.sh --unit 'cargo example --lib' --unit 'cargo .' --unit 'cargo example --test wire' >"$work/output"
[ "$(grep -c . "$LAND_CHECK_LOG")" = 1 ] && grep -qE '^capture in native: cargo --receipt-out [^ ]+ -- --no-fail-fast --workspace$' "$LAND_CHECK_LOG" || { echo 'cargo . did not run the whole workspace once, from native/, in place of the narrower cargo units' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
: >"$LAND_CHECK_LOG"
scripts/landing-tests.sh --unit 'cargo example --test wire' >"$work/output"
grep -qF -- "--unit cargo example --test wire --receipt-out" "$LAND_CHECK_LOG" && grep -qF -- '-- --no-fail-fast -p example --test wire' "$LAND_CHECK_LOG" || { echo 'a cargo unit did not run its own target' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
if scripts/landing-tests.sh --unit 'cargo example' >"$work/output" 2>&1; then echo 'a cargo unit without a target ran' >&2; exit 1; fi
expect_output "'cargo .', 'cargo <package> --lib', 'cargo <package> --doc' or 'cargo <package> --test|--bin <name>'"

echo 'landing fast gate: OK'
