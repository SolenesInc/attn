#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-land-test.XXXXXX")"
release_held_checks() {
  local fifo
  for fifo in "$work/soak-release"; do
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
if [ "$1" = generate-types ]; then
  printf 'regenerated\n' >internal/protocol/generated.go
  exit 0
fi
if [ "$1" = generate-prompts ]; then
  printf '{"regenerated":true}\n' >internal/prompts/catalog.generated.json
  exit 0
fi
if [ -p "${LAND_GATE_AWAITS_SOAK:-}" ]; then
  read -r _ <"$LAND_GATE_AWAITS_SOAK"
  rm "$LAND_GATE_AWAITS_SOAK"
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
    shift 3
    units_out=""
    groups=()
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --units-out) units_out="$2"; shift 2 ;;
        --main) shift 2 ;;
        --group) groups+=(--group "$2"); shift 2 ;;
        *) echo "unexpected test-history land arg $1" >&2; exit 2 ;;
      esac
    done
    printf '%s\n' "land ${groups[*]}" >>"$LAND_CHECK_LOG"
    [ "${LAND_SELECT_EXIT:-0}" -eq 0 ] || exit "$LAND_SELECT_EXIT"
    [ -n "$units_out" ] || exit 2
    printf '%s' "${LAND_SELECTED-$selected}" >"$units_out"
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
export PATH="$work/bin:$PATH"
export LAND_CHECK_LOG="$work/checks" LAND_OTHER="$work/other"
export LAND_REMOTE="$work/remote" LAND_SOAK_REFS="$work/soak-refs"
export selected=$'go ./internal/example TestExample\ncargo example --lib'
selected_units="--unit go ./internal/example TestExample --unit cargo example --lib"
go_choice=('land --group go --group prompts' "landing-tests --no-go-vet $selected_units")
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git init -q --bare "$work/remote"
cd "$work/repo"
git init -q
git config user.name 'Landing Test'
git config user.email 'landing@example.invalid'
git config core.hooksPath /dev/null
git switch -qc work
mkdir -p internal/example internal/removed scripts internal/protocol/schema internal/prompts
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
cp "$root/scripts/land-linux.sh" scripts/land-linux.sh
cp "$root/scripts/land-resolve.sh" scripts/land-resolve.sh
cp "$root/scripts/land-record.sh" scripts/land-record.sh
mkdir -p app/scripts/real-app-harness
cp "$root"/app/scripts/real-app-harness/{landing-scenarios,scenarioCatalog,matrixDigest}.mjs app/scripts/real-app-harness/
git add .
git commit -qm baseline
git remote add origin "$work/remote"
git push -q origin HEAD:main
git clone -q -b main "$work/remote" "$LAND_OTHER"
git -C "$LAND_OTHER" config user.name 'Other Landing'
git -C "$LAND_OTHER" config user.email 'other@example.invalid'

expect_output() {
  grep -qF -- "$1" "$work/output" || { echo "missing from the landing output: $1" >&2; cat "$work/output" >&2; exit 1; }
}

# 1. Retired scripts/land.sh refuses to run and guides callers to `land`.
if bash "$root/scripts/land.sh" >"$work/output" 2>&1; then
  echo 'retired scripts/land.sh exited 0' >&2
  exit 1
fi
expect_output 'scripts/land.sh is retired'
expect_output 'land guide'
expect_output 'ATTN_LAND_BASELINE=1 land'
expect_output 'If git'"'"'s pre-push hook sent you here after land'"'"'s gate passed'

# 2. .githooks/pre-push refuses plain pushes and ATTN_LANDING=1, and allows LAND_LANDING=1.
if printf 'refs/heads/work 1 refs/heads/main 2\n' | bash "$root/.githooks/pre-push" >"$work/output" 2>&1; then
  echo 'pre-push allowed a plain push to main' >&2
  exit 1
fi
expect_output 'Land on main with land'
if printf 'refs/heads/work 1 refs/heads/main 2\n' | ATTN_LANDING=1 bash "$root/.githooks/pre-push" >"$work/output" 2>&1; then
  echo 'pre-push still allowed ATTN_LANDING=1' >&2
  exit 1
fi
printf 'refs/heads/work 1 refs/heads/main 2\n' | LAND_LANDING=1 bash "$root/.githooks/pre-push"

# 3. scripts/land-record.sh forwards arguments to scripts/pr-evidence.sh into $LAND_MEDIA.
if bash scripts/land-record.sh demo --instance dev >"$work/output" 2>&1; then
  echo 'land-record.sh succeeded without LAND_MEDIA' >&2
  exit 1
fi
cat >scripts/pr-evidence.sh <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$LAND_MEDIA/called.txt"
EOF
chmod +x scripts/pr-evidence.sh
mkdir -p "$work/media"
LAND_MEDIA="$work/media" bash scripts/land-record.sh demo --instance dev --seconds 5
grep -qxF "record --instance dev --seconds 5 --out $work/media/demo.mp4" "$work/media/called.txt"
# Empty optional flags must not crash under set -u on Bash 3.2.
LAND_MEDIA="$work/media" bash scripts/land-record.sh plain
grep -qxF "record --out $work/media/plain.mp4" "$work/media/called.txt"
# Reject path separators, dotfiles, and empty names.
for bad_name in 'sub/demo' '.hidden' '.mp4'; do
  if LAND_MEDIA="$work/media" bash scripts/land-record.sh "$bad_name" --instance dev >"$work/output" 2>&1; then
    echo "land-record.sh accepted invalid clip name $bad_name" >&2
    exit 1
  fi
done
rm -f scripts/pr-evidence.sh

# 4. scripts/land-resolve.sh regenerates generated conflicts (types before prompts) and refuses handwritten conflicts.
printf 'base\n' >internal/protocol/generated.go
printf '{"base":true}\n' >internal/prompts/catalog.generated.json
git add internal/protocol/generated.go internal/prompts/catalog.generated.json
git commit -qm 'Add generated fixtures'
git push -q origin HEAD:main
git -C "$LAND_OTHER" pull -q origin main
printf 'theirs\n' >"$LAND_OTHER/internal/protocol/generated.go"
printf '{"theirs":true}\n' >"$LAND_OTHER/internal/prompts/catalog.generated.json"
git -C "$LAND_OTHER" commit -qam 'Theirs generated'
git -C "$LAND_OTHER" push -q origin HEAD:main
printf 'ours\n' >internal/protocol/generated.go
printf '{"ours":true}\n' >internal/prompts/catalog.generated.json
git commit -qam 'Ours generated'
git fetch -q origin main
git rebase -q origin/main >/dev/null 2>&1 || true
: >"$LAND_CHECK_LOG"
bash scripts/land-resolve.sh >"$work/output" 2>&1 || { cat "$work/output" >&2; exit 1; }
diff -u <(printf 'generate-types\ngenerate-prompts\n') "$LAND_CHECK_LOG"
[ -z "$(git diff --name-only --diff-filter=U)" ]
GIT_EDITOR=true git rebase --continue >/dev/null 2>&1
grep -qxF 'regenerated' internal/protocol/generated.go
grep -qxF '{"regenerated":true}' internal/prompts/catalog.generated.json
git push -q origin HEAD:main

git -C "$LAND_OTHER" pull -q origin main
printf 'theirs-handwritten\n' >"$LAND_OTHER/README.md"
git -C "$LAND_OTHER" commit -qam 'Theirs README'
git -C "$LAND_OTHER" push -q origin HEAD:main
printf 'ours-handwritten\n' >README.md
git commit -qam 'Ours README'
git fetch -q origin main
git rebase -q origin/main >/dev/null 2>&1 || true
if bash scripts/land-resolve.sh >"$work/output" 2>&1; then
  echo 'land-resolve.sh accepted a handwritten conflict' >&2
  exit 1
fi
git rebase --abort
git reset -q --hard origin/main

# 5. scripts/land-gate.sh path selection, landing tests, and scenario runner behavior.
gate() {
  local message="$1"
  shift
  : >"$LAND_CHECK_LOG"
  git add -A
  git commit -qm "$message"
  env LAND_BASE="$(git rev-parse origin/main)" "$@" bash "$root/scripts/land-gate.sh" ${LAND_FLAGS:-} >"$work/output" 2>&1 || { cat "$work/output" >&2; return 1; }
  git push -q origin HEAD:main
}

go_checks_last() {
  awk '/^check-go / { last = last $0 "\n"; next } { print } END { printf "%s", last }' "$1"
}

expect_checks() {
  printf '%s\n' "$@" >"$work/expected"
  diff -u <(go_checks_last "$work/expected") <(go_checks_last "$LAND_CHECK_LOG")
}

printf '\n' >>internal/example/example.go
gate 'Go change'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

git rm -q internal/example/other.go internal/removed/removed.go
gate 'Go deletions'
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync

printf '\n' >>go.mod
gate 'Module change'
expect_checks "${go_choice[0]}" 'check-types' 'check-go GO_PACKAGES=./...' "${go_choice[1]}" sync

mkdir -p app/scripts/real-app-harness
printf 'export {};\n' >app/scripts/real-app-harness/example.mjs
gate 'Harness change'
expect_checks 'check-harness lint-js' sync

mkdir -p native/browser-runtime/src
printf 'export {};\n' >native/browser-runtime/src/example.ts
gate 'Browser runtime change'
expect_checks 'land --group native' 'lint-harness-apps check-browser-runtime check-native NATIVE_CLIPPY=' "landing-tests $selected_units" sync

mkdir -p native/crates/demo/src
printf '[workspace]\n' >native/Cargo.toml
printf '[package]\n' >native/crates/demo/Cargo.toml
printf '\n' >native/crates/demo/src/lib.rs
gate 'Native workspace arrives'
expect_checks 'land --group native' 'lint-harness-apps check-native NATIVE_CLIPPY=--workspace' "landing-tests $selected_units" sync

printf '\n' >>native/crates/demo/src/lib.rs
gate 'Native crate change'
expect_checks 'land --group native' 'lint-harness-apps check-native NATIVE_CLIPPY=-p demo' "landing-tests $selected_units" sync

mkdir -p mobile/src
printf 'export {};\n' >mobile/src/example.ts
gate 'Mobile change'
expect_checks 'lint-harness-apps check-mobile' sync

mkdir -p pty-host/src
printf '\n' >pty-host/src/example.rs
gate 'PTY host change'
expect_checks 'land --group pty-host' lint-harness-apps "landing-tests $selected_units" sync

mkdir -p scripts/harnesslint
printf '\n' >scripts/harnesslint/allow.txt
gate 'Harness lint allowance change'
expect_checks lint-harness sync

mkdir -p sdk/plugin/src
printf 'export {};\n' >sdk/plugin/src/example.ts
gate 'SDK change'
expect_checks lint-js sync

mkdir -p scripts internal/protocol/schema internal/prompts
printf 'true\n' >scripts/example.sh
printf 'schema\n' >internal/protocol/schema/example.tsp
printf 'prompt\n' >internal/prompts/example.md
gate 'Scripts and generated inputs'
expect_checks 'land --group prompts --group native' 'check-types check-prompts check-native NATIVE_CLIPPY=' "landing-tests $selected_units" sync
expect_output 'the checkout has no script tests'

mkdir -p internal/setting
printf 'setting\n' >internal/setting/example.txt
gate 'Settings declaration change'
expect_checks 'check-types' sync

printf 'Home Gateway\n' >internal/protocol/schema/home_gateway.tsp
gate 'HomeGateway API change'
expect_checks 'land --group old-phones --group native' 'check-types check-home-gateway-compat check-native NATIVE_CLIPPY=' "landing-tests $selected_units" sync

mkdir -p scripts/lib
printf 'true\n' >scripts/lib/shared.sh
printf 'bash scripts/example.sh\nsource scripts/lib/shared.sh\n' >scripts/example_test.sh
printf 'source scripts/lib/shared.sh\n' >scripts/other_test.sh
printf 'true\n' >scripts/unrelated_test.sh
gate 'Script tests arrive'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh scripts/other_test.sh scripts/unrelated_test.sh' sync

printf '\n' >>scripts/example.sh
gate 'Script change'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh' sync
expect_output 'script test scripts/example_test.sh, for scripts/example.sh'

printf '\n' >>scripts/lib/shared.sh
gate 'Shared script change'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh scripts/other_test.sh' sync

mkdir -p .github/workflows fixtures
printf 'on: push\n' >.github/workflows/example.yml
printf 'one\n' >fixtures/one.txt
printf 'grep -q push "$root/.github/workflows/example.yml"\ncp -R "$root/fixtures" "$work"\n' >scripts/reads_test.sh
gate 'Script test that reads checkout files arrives'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/reads_test.sh' sync

printf '\n' >>scripts/example.sh
printf '\n' >>.github/workflows/example.yml
gate 'Script and workflow change'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/example_test.sh scripts/reads_test.sh' sync
expect_output 'script test scripts/reads_test.sh, for .github/workflows/example.yml'

printf 'two\n' >fixtures/two.txt
gate 'Change inside a directory a script test reads'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/reads_test.sh' sync

printf 'source scripts/lib/deep.sh\n' >scripts/chain.sh
printf 'true\n' >scripts/lib/deep.sh
printf 'bash scripts/chain.sh\n' >scripts/chain_test.sh
gate 'Chained script test arrives'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/chain_test.sh' sync

printf '\n' >>scripts/lib/deep.sh
gate 'Change to a script a tested script sources'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/chain_test.sh' sync
expect_output 'script test scripts/chain_test.sh, for scripts/lib/deep.sh'

printf 'true\n' >scripts/lonely.sh
gate 'Script no test names'
expect_checks 'test-scripts SCRIPT_TESTS=scripts/chain_test.sh scripts/example_test.sh scripts/other_test.sh scripts/reads_test.sh scripts/unrelated_test.sh' sync
expect_output 'no script test names scripts/lonely.sh; running every script test'

printf '\n' >>README.md
gate 'Documentation change'
expect_checks sync

no_land_scenario_refs() {
  [ -z "$(git --git-dir="$work/remote" for-each-ref refs/heads/land-scenarios)" ] || { echo 'the scenario run left its ref on the remote' >&2; exit 1; }
}

mkdir -p app/scripts/real-app-harness
printf '// changed\n' >app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
: >"$LAND_SOAK_REFS"
gate 'Change a scenario'
grep -qF 'soak workflow run acceptance-soak.yml' "$LAND_CHECK_LOG" || { echo 'a changed scenario did not run before the push' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF -- '-f scenarios=native-shelf-moves -f repeat=1' "$LAND_CHECK_LOG" || { echo 'the soak did not name the changed scenario' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF 'check-harness' "$LAND_CHECK_LOG" || { echo 'the gate did not run beside the scenarios' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF refs/heads/land-scenarios/ "$LAND_SOAK_REFS" || { echo 'the soak ran before its ref reached the remote' >&2; exit 1; }
no_land_scenario_refs
expect_output 'passed on CI'

printf '// changed\n' >app/scripts/real-app-harness/scenario-native-quick-capture.mjs
gate 'Change a macOS-only scenario'
if grep -qF 'soak' "$LAND_CHECK_LOG"; then echo 'a macOS-only scenario ran on Linux' >&2; exit 1; fi
expect_output 'scenario native-quick-capture changed and does not run on Linux'

mkdir -p .agents/skills/drive-attn/features
printf '## Tests\n\n- Wire: `internal/example/example_wire_test.go`.\n- Scenario: `native-shelf-layout`, `native-quick-capture`, `native-shelf-peek`.\n' >.agents/skills/drive-attn/features/example.md
gate 'Reword a feature file without changing the product'
if grep -qF 'soak' "$LAND_CHECK_LOG"; then echo 'a feature file alone ran its scenarios' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; fi

printf '\n' >>internal/example/example.go
gate 'Change the product without changing a feature file'
if grep -qF 'soak' "$LAND_CHECK_LOG"; then echo 'a product change alone ran a scenario' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; fi

printf '\n' >>internal/example/example.go
printf '\n' >>.agents/skills/drive-attn/features/example.md
gate 'Change the product and the feature file that describes it'
grep -qF -- '-f scenarios=native-shelf-peek,native-shelf-layout -f repeat=1' "$LAND_CHECK_LOG" || { echo 'the soak did not name the feature file scenarios that run on Linux' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
no_land_scenario_refs

printf '\n' >>internal/example/example.go
ATTN_LAND_BASELINE=1 gate 'Go change landed on the baseline'
expect_checks 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts'
expect_output 'running the baseline landing tests (ATTN_LAND_BASELINE=1)'

printf '\n' >>internal/example/example.go
gate 'Go change when choosing fails' LAND_SELECT_EXIT=1
expect_checks 'land --group go --group prompts' 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts' sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

printf '\n' >>internal/example/example.go
gate 'Go change when sync fails' LAND_SYNC_EXIT=1
expect_checks 'check-go GO_PACKAGES=./internal/example' "${go_choice[@]}" sync
expect_output "this landing's test receipts upload at the next sync"

printf '\n' >>internal/example/example.go
gate 'Go change when choosing writes nothing' LAND_SELECTED=
expect_checks 'land --group go --group prompts' 'check-go GO_PACKAGES=./internal/example' 'landing-tests --no-go-vet go prompts' sync
expect_output 'choosing landing tests failed; running the baseline groups go prompts'

refuses() {
  local changing="${3:-internal/example/example.go}"
  printf '\n' >>"$changing"
  git add -A
  git commit -qm "$1"
  if env LAND_BASE="$(git rev-parse origin/main)" "$2" bash "$root/scripts/land-gate.sh" >"$work/failure" 2>&1; then
    echo "gate exited 0 despite $1" >&2
    exit 1
  fi
  git reset -q --hard origin/main
}

refuses 'a failed Go check' LAND_CHECK_EXIT=1 internal/example/example.go
grep -qF 'make ran: check-go' "$work/failure" || { echo 'the failed Go check left no output' >&2; cat "$work/failure" >&2; exit 1; }
refuses 'a failed landing test beside passing Go checks' LAND_TESTS_EXIT=1 internal/example/example.go
grep -qF 'make ran: check-go' "$work/failure" || { echo 'the Go checks left no output' >&2; cat "$work/failure" >&2; exit 1; }
refuses 'a changed scenario that fails on CI' LAND_SOAK_EXIT=1 app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
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
printf '// gate fails\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
git add -A
git commit -qm 'Scenario change whose gate fails while its scenario runs'
: >"$LAND_CHECK_LOG"
if env LAND_BASE="$(git rev-parse origin/main)" LAND_SOAK_HOLD="$work/soak-hold" LAND_GATE_AWAITS_SOAK="$work/soak-hold/watching" LAND_CHECK_EXIT=1 bash "$root/scripts/land-gate.sh" >"$work/output" 2>&1; then
  echo 'a gate failure exited 0' >&2
  exit 1
fi
grep -qxF 'soak run cancel 7' "$LAND_CHECK_LOG" || { echo 'a failed gate did not cancel the running scenarios' >&2; cat "$LAND_CHECK_LOG" "$work/output" >&2; exit 1; }
expect_output 'stopped the scenario step'
no_land_scenario_refs
[ ! -e "$(record_for_landing)" ] || { echo 'a stopped scenario step recorded a pass' >&2; exit 1; }

# Scenario pass record survives a rebase with the same diff, and invalidates when diff changes.
: >"$LAND_CHECK_LOG"
env LAND_BASE="$(git rev-parse origin/main)" bash "$root/scripts/land-gate.sh" >"$work/output" 2>&1 || { cat "$work/output" >&2; exit 1; }
grep -qF 'soak workflow run' "$LAND_CHECK_LOG" || { echo 'the scenario did not run' >&2; cat "$work/output" >&2; exit 1; }
grep -qxF 'native-shelf-moves github https://example.invalid/runs/7' "$(record_for_landing)" || { echo 'a passed scenario left no pass record' >&2; exit 1; }
git -C "$LAND_OTHER" pull -q origin main
printf 'moved\n' >"$LAND_OTHER/moved.md"
git -C "$LAND_OTHER" add -A
git -C "$LAND_OTHER" commit -qm 'Main moves before the gate runs again'
git -C "$LAND_OTHER" push -q origin HEAD:main
git fetch -q origin main
git rebase -q origin/main
: >"$LAND_CHECK_LOG"
env LAND_BASE="$(git rev-parse origin/main)" bash "$root/scripts/land-gate.sh" >"$work/output" 2>&1 || { cat "$work/output" >&2; exit 1; }
if grep -qF soak "$LAND_CHECK_LOG"; then echo 'an unchanged diff ran its scenario again after a rebase' >&2; cat "$work/output" >&2; exit 1; fi
expect_output 'scenario native-shelf-moves passed on this diff already: https://example.invalid/runs/7'
grep -qF 'check-harness' "$LAND_CHECK_LOG" || { echo 'a gate with recorded scenarios skipped its checks' >&2; exit 1; }
printf 'changed diff\n' >>README.md
git add -A
git commit -q --amend --no-edit
: >"$LAND_CHECK_LOG"
env LAND_BASE="$(git rev-parse origin/main)" bash "$root/scripts/land-gate.sh" >"$work/output" 2>&1 || { cat "$work/output" >&2; exit 1; }
grep -qF 'soak workflow run' "$LAND_CHECK_LOG" || { echo 'a changed diff did not run its scenario again' >&2; cat "$work/output" >&2; exit 1; }
git push -q origin HEAD:main

recently_failed="$selected"$'\nnative native-shelf-peek\nnative native-quick-capture\nnative retired-scenario'
expect_recent_scenarios_ran() {
  grep -qxF "${go_choice[1]}" "$LAND_CHECK_LOG" || { echo 'the landing tests were not handed exactly the units that are not scenarios' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
  expect_output 'scenario native-quick-capture failed on main recently and does not run on Linux'
  expect_output 'scenario retired-scenario failed on main recently and is no longer in the scenario catalog'
}
printf '\n' >>internal/example/example.go
gate 'Go change while a scenario that failed on main has since passed there' LAND_SELECTED="$recently_failed"
grep -qF -- '-f scenarios=native-shelf-peek -f repeat=1' "$LAND_CHECK_LOG" || { echo 'the scenario step did not run the scenario the landing tests chose' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'land: scenarios native-shelf-peek passed on CI'"'"'s Linux'
expect_recent_scenarios_ran
no_land_scenario_refs

git config attn.landRunner ssh:runner.invalid
printf '// on the landing runner\n' >>internal/example/example.go
gate 'Go change on the landing runner while a scenario that failed on main has since passed there' LAND_SELECTED="$recently_failed"
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 --scenario native-shelf-peek --out ' "$LAND_CHECK_LOG" || { echo 'the landing runner did not run the scenario the landing tests chose' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario native-shelf-peek passed on runner.invalid in 2s'
expect_recent_scenarios_ran

printf '\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
gate 'Change a scenario on the landing runner' LAND_RUNNER_WAITED=7
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 --scenario native-shelf-moves --out ' "$LAND_CHECK_LOG" || { echo 'the scenario did not run on the landing runner' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
if grep -qF 'workflow run' "$LAND_CHECK_LOG"; then echo 'a scenario that passed on the landing runner ran on CI' >&2; exit 1; fi
expect_output 'scenario native-shelf-moves passed on runner.invalid in 2s'
expect_output 's, 7s of it waiting for other landings'
grep -rqE '^native-shelf-moves ssh:runner.invalid /.*/runner.log$' "$records" || { echo 'a scenario that passed on the landing runner left no pass record' >&2; exit 1; }

printf '\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
printf '// busy runner\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
gate 'Change a scenario while the landing runner stays busy past the turn limit' LAND_RUNNER_GAVE_UP=655
grep -qF -- '-f scenarios=native-shelf-moves -f repeat=1' "$LAND_CHECK_LOG" || { echo 'a landing that gave up its wait for the landing runner did not run on CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output "waited 655s for a turn on the landing runner runner.invalid, past its 654s limit, so the scenarios run on CI's Linux instead"
grep -qE '^native-shelf-moves github ' "$(ls -t "$records"/* | head -1)" || { echo 'a scenario that passed on CI after the wait left no pass record' >&2; exit 1; }

printf '\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
gate 'Change a scenario while the landing runner is down' LAND_RUNNER_REACH_EXIT=255
if grep -qF runner "$LAND_CHECK_LOG"; then echo 'a scenario ran on an unreachable landing runner' >&2; exit 1; fi
grep -qF -- '-f scenarios=native-shelf-moves -f repeat=1' "$LAND_CHECK_LOG" || { echo 'an unreachable landing runner did not fall back to CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'the landing runner runner.invalid cannot be reached'

git config --add attn.landRunner ssh:second.invalid
printf '// two runners\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
gate 'Change a scenario while the first of two landing runners is busy' LAND_RUNNER_BUSY=runner.invalid
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 0 ' "$LAND_CHECK_LOG" || { echo 'a landing with two runners waited on the first one' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'the landing runner runner.invalid is busy with another landing'
expect_output 'scenario native-shelf-moves passed on second.invalid in 2s'
if grep -qF 'workflow run' "$LAND_CHECK_LOG"; then echo 'a scenario that passed on the second landing runner ran on CI' >&2; exit 1; fi
grep -rqE '^native-shelf-moves ssh:second.invalid ' "$records" || { echo 'a scenario that passed on the second landing runner left no pass record' >&2; exit 1; }

printf '// both busy\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
gate 'Change a scenario while both landing runners are busy' LAND_RUNNER_BUSY=runner.invalid LAND_RUNNER_ALSO_BUSY=second.invalid
grep -qE '^runner soak --provider ssh --target runner.invalid --checkout land --turn-limit 654 ' "$LAND_CHECK_LOG" || { echo 'a landing that found every runner busy did not wait for the first' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario native-shelf-moves passed on runner.invalid in 2s'
git config --unset-all attn.landRunner
git config attn.landRunner ssh:runner.invalid

printf '\n' >>internal/example/example.go
printf '\n' >>.agents/skills/drive-attn/features/example.md
gate 'Change the product while a scenario fails on the landing runner' LAND_RUNNER_FAIL=native-shelf-peek
[ "$(grep -c '^runner soak ' "$LAND_CHECK_LOG")" = 1 ] && grep -qE -- '--scenario (native-shelf-layout,native-shelf-peek|native-shelf-peek,native-shelf-layout) ' "$LAND_CHECK_LOG" || { echo 'the landing runner did not run every scenario in one run' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
grep -qF -- '-f scenarios=native-shelf-peek -f repeat=1' "$LAND_CHECK_LOG" || { echo 'only the scenario that failed on the landing runner should run on CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario native-shelf-peek failed on runner.invalid in 3s, so it runs again on CI'
grep -rqE '^native-shelf-layout ssh:runner.invalid ' "$records" || { echo 'a scenario that passed beside a failed one left no pass record' >&2; exit 1; }
no_land_scenario_refs

printf '// runner dies\n' >>internal/example/example.go
printf 'runner dies\n' >>.agents/skills/drive-attn/features/example.md
gate 'Change the product while the landing runner dies before its last scenario' LAND_RUNNER_CRASH_AT=native-shelf-layout
grep -qF -- '-f scenarios=native-shelf-layout -f repeat=1' "$LAND_CHECK_LOG" || { echo 'a scenario the landing runner did not finish should run on CI' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
expect_output 'scenario native-shelf-layout did not finish on runner.invalid, so it runs again on CI'
record="$(grep -rlE '^native-shelf-layout github ' "$records" | xargs ls -t | head -1)"
grep -qE '^native-shelf-peek ssh:runner.invalid ' "$record" || { echo 'the scenario the landing runner finished left no pass record' >&2; cat "$record" >&2; exit 1; }
if grep -qE '^native-shelf-layout ssh:' "$record"; then echo 'a scenario the landing runner did not finish was recorded as passed there' >&2; exit 1; fi

export LAND_RUNNER_FAIL=native-shelf-moves
refuses 'a scenario that fails on the landing runner and on CI' LAND_SOAK_EXIT=1 app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
unset LAND_RUNNER_FAIL
grep -qF 'gh run view 7 --log-failed' "$work/failure" || { echo 'the scenario that failed on CI did not block the landing' >&2; cat "$work/failure" >&2; exit 1; }
no_land_scenario_refs

mkdir "$work/runner-hold"
mkfifo "$work/runner-hold/watching" "$work/runner-hold/release"
printf '// gate fails on the runner\n' >>app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
printf '// gate fails on the runner\n' >>app/scripts/real-app-harness/scenario-native-shelf-peek.mjs
git add -A
git commit -qm 'Scenario change whose gate fails while it runs on the landing runner'
: >"$LAND_CHECK_LOG"
if env LAND_BASE="$(git rev-parse origin/main)" LAND_RUNNER_HOLD="$work/runner-hold" LAND_GATE_AWAITS_SOAK="$work/runner-hold/watching" LAND_CHECK_EXIT=1 bash "$root/scripts/land-gate.sh" >"$work/output" 2>&1; then
  echo 'a gate failure exited 0' >&2
  exit 1
fi
grep -qxF 'runner child stopped' "$LAND_CHECK_LOG" || { echo 'a failed gate did not stop the processes of the scenario on the landing runner' >&2; cat "$LAND_CHECK_LOG" "$work/output" >&2; exit 1; }
first="$(sed -n 's/^runner soak .*--scenario \([^,]*\),.*/\1/p' "$LAND_CHECK_LOG")"
[ -n "$first" ] || { echo 'the landing runner did not run both scenarios in one run' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; }
[ "$(cut -d' ' -f1 "$(record_for_landing)")" = "$first" ] || { echo 'a stopped landing runner must keep the pass it finished and record no other' >&2; cat "$(record_for_landing)" >&2; exit 1; }
git reset -q --hard origin/main

printf '// native\n' >>native/crates/demo/src/lib.rs
gate 'Native change' LAND_SELECTED='cargo .'
if grep -qF runner "$LAND_CHECK_LOG"; then echo 'a native change ran the wire suite on the landing runner, though the wire suite gives the same result on every host' >&2; cat "$LAND_CHECK_LOG" >&2; exit 1; fi

git config attn.landRunner runner.invalid
refuses 'a landing runner that names no runner' GIT_CONFIG_NOSYSTEM=1 app/scripts/real-app-harness/scenario-native-shelf-moves.mjs
grep -qF 'set it to github or ssh:<target>' "$work/failure" || { cat "$work/failure" >&2; exit 1; }
git config --unset attn.landRunner

# 6. scripts/landing-tests.sh standalone options.
mkdir -p "$work/tests-repo/scripts"
cd "$work/tests-repo"
git init -q
cp "$root/scripts/landing-tests" "$root/scripts/landing-tests.sh" scripts/
cat >scripts/test-go.sh <<'EOF'
#!/usr/bin/env bash
if [ "$1" = --provide-test-binaries ]; then
  printf 'fake-attn\nfake-agent\nfake-host\n\n\n' >"$2"
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

# Failing Go and cargo units print diagnostic repro and history commands rather than a single-run rerun.
cat >"$work/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[ "$1 $2" = "tool test-history" ] || { echo "unexpected go $*" >&2; exit 2; }
case "$3" in
  capture)
    while [ "$#" -gt 0 ]; do
      [ "$1" != --receipt-out ] || : >"$2"
      shift
    done
    exit 1
    ;;
  summary)
    if [ "${LAND_SUMMARY_KIND:-go}" = cargo ]; then
      echo 'FAIL cargo example --test wire (0.2s)'
    else
      echo 'FAIL go ./internal/example TestExample (0.2s)'
    fi
    exit 1
    ;;
esac
EOF
chmod +x "$work/bin/go"
if scripts/landing-tests.sh --unit 'go ./internal/example TestExample' >"$work/output" 2>&1; then
  echo 'a failing Go unit exited 0' >&2
  exit 1
fi
expect_output "repro: ./scripts/test-go.sh --packages -count=10 -race -run '^TestExample$' ./internal/example"
expect_output "history: go tool test-history explain --test 'go ./internal/example TestExample'"
if grep -qF -- '-count=1 ' "$work/output"; then
  echo 'failing Go unit still suggested a single-run -count=1 rerun' >&2
  cat "$work/output" >&2
  exit 1
fi
if LAND_SUMMARY_KIND=cargo scripts/landing-tests.sh --unit 'cargo example --test wire' >"$work/output" 2>&1; then
  echo 'a failing cargo unit exited 0' >&2
  exit 1
fi
expect_output 'repro: (cd native && cargo test -p example --test wire)'
expect_output "history: go tool test-history explain --test 'cargo example --test wire'"

echo 'landing fast gate: OK'
