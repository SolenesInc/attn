#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-ci-acceptance-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    echo "expected failure: $*" >&2
    exit 1
  fi
}

workflow="$root/.github/workflows/ci.yml"
workflow_concurrency="$(sed -n '/^concurrency:/,/^jobs:/p' "$workflow")"
for contract in \
  'group: ci-${{ github.event.pull_request.number || github.ref }}' \
  "cancel-in-progress: \${{ github.ref != 'refs/heads/main' }}"; do
  if ! grep -Fq "$contract" <<<"$workflow_concurrency"; then
    echo "CI concurrency is missing: $contract" >&2
    exit 1
  fi
done

app_acceptance_job="$(sed -n '/^  app-acceptance:/,/^  release-preflight:/p' "$workflow")"
if grep -Eq '^    concurrency:' <<<"$app_acceptance_job"; then
  echo "App acceptance must not serialize independent hosted runners" >&2
  exit 1
fi
for contract in \
  'runs-on: ubuntu-24.04' \
  "run: xvfb-run -a -s '-screen 0 1600x1000x24' pnpm --dir app run real-app:serial-matrix"; do
  if ! grep -Fq "$contract" <<<"$app_acceptance_job"; then
    echo "App acceptance must keep its isolated display and serial matrix: $contract" >&2
    exit 1
  fi
done

for contract in \
  'continue-on-error: true' \
  'name: Gate app acceptance' \
  "if: needs.changes.outputs.force_all == 'true' || needs.changes.outputs.harness == 'true'"; do
  if ! grep -Fq "$contract" <<<"$app_acceptance_job"; then
    echo "App acceptance needs its path policy, and its verdict in the gate step" >&2
    echo "rather than the continue-on-error matrix: $contract" >&2
    exit 1
  fi
done

changelog_job="$(sed -n '/^  changelog:/,/^  main-route:/p' "$workflow")"
if ! grep -Fq 'app-acceptance' <<<"$changelog_job" ||
  ! grep -Fq '!cancelled()' <<<"$changelog_job"; then
  echo "Changelog must wait on App acceptance and survive it being filtered out" >&2
  exit 1
fi

react_doctor="$root/.github/workflows/react-doctor.yml"
react_doctor_triggers="$(sed -n '/^on:/,/^permissions:/p' "$react_doctor")"
if ! grep -Fq 'pull_request:' <<<"$react_doctor_triggers" ||
  grep -Fq 'push:' <<<"$react_doctor_triggers"; then
  echo "React Doctor must run on pull requests only" >&2
  exit 1
fi

changes_job="$(sed -n '/^  changes:/,/^  changelog:/p' "$workflow")"
if ! grep -Fq 'fetch-depth: 0' <<<"$changes_job" ||
  ! grep -Fq "token: ''" <<<"$changes_job"; then
  echo "Changes must classify PR paths from full local git history" >&2
  exit 1
fi
if ! grep -Fq "              - 'scripts/**'" "$workflow"; then
  echo "Daemon path filter must cover every repository script" >&2
  exit 1
fi
if ! grep -Fq 'harness: ${{ steps.filter.outputs.harness }}' <<<"$changes_job"; then
  echo "Changes must publish the harness filter App acceptance gates on" >&2
  exit 1
fi
for path in \
  "app/scripts/real-app-harness/**" \
  "app/src-tauri/**" \
  "apphost/**" \
  "scripts/build-app-runtime-host.sh" \
  ".github/workflows/app-acceptance.yml" \
  ".github/workflows/ci.yml"; do
  if ! grep -Fq "              - '$path'" <<<"$(sed -n '/^            harness:/,/^            release_preflight:/p' "$workflow")"; then
    echo "Harness path filter must cover: $path" >&2
    exit 1
  fi
done

sed -n '/^jobs:/,$p' "$workflow" \
  | sed -nE 's/^  ([a-z0-9-]+):$/\1/p' \
  | grep -Ev '^(pr-gate|acceptance|branch-health)$' \
  | sort >"$work/leaf-jobs"

extract_needs() {
  local target="$1"
  awk -v target="$target" '
    $0 == "  " target ":" { in_job = 1; next }
    in_job && /^  [a-z0-9-]+:$/ { exit }
    in_job && /^    needs:$/ { in_needs = 1; next }
    in_needs && /^      - / { sub(/^      - /, ""); print; next }
    in_needs { exit }
  ' "$workflow" | sort
}

for gate in pr-gate acceptance; do
  extract_needs "$gate" >"$work/$gate-needs"
  if ! diff -u "$work/leaf-jobs" "$work/$gate-needs"; then
    echo "$gate does not aggregate every CI leaf job" >&2
    exit 1
  fi
done

branch_health_job="$(sed -n '/^  branch-health:/,$p' "$workflow")"
for contract in \
  'id: branch-health-issue' \
  'continue-on-error: true' \
  "if: steps.branch-health-issue.outcome == 'failure'" \
  'Acceptance is' \
  'authoritative; the branch-health issue could not be updated.'; do
  if ! grep -Fq "$contract" <<<"$branch_health_job"; then
    echo "branch-health reporter is missing: $contract" >&2
    exit 1
  fi
done

success='{"changes":{"result":"success"},"backend":{"result":"success"}}'
filtered='{"changes":{"result":"success"},"backend":{"result":"skipped"}}'
failed='{"changes":{"result":"success"},"backend":{"result":"failure"}}'
cancelled='{"app-acceptance":{"result":"cancelled"}}'
missing='{"changes":{"result":"success"}}'

NEEDS_JSON="$success" "$root/scripts/ci-gate.sh" acceptance changes backend >/dev/null
NEEDS_JSON="$filtered" "$root/scripts/ci-gate.sh" pr changes backend >/dev/null
expect_failure env NEEDS_JSON="$filtered" "$root/scripts/ci-gate.sh" acceptance changes backend
expect_failure env NEEDS_JSON="$failed" "$root/scripts/ci-gate.sh" pr changes backend
expect_failure env NEEDS_JSON="$cancelled" "$root/scripts/ci-gate.sh" pr app-acceptance
skipped_acceptance='{"app-acceptance":{"result":"skipped"}}'
NEEDS_JSON="$skipped_acceptance" "$root/scripts/ci-gate.sh" pr app-acceptance >/dev/null
expect_failure env NEEDS_JSON="$skipped_acceptance" "$root/scripts/ci-gate.sh" acceptance app-acceptance
expect_failure env NEEDS_JSON="$cancelled" "$root/scripts/ci-gate.sh" acceptance app-acceptance
expect_failure env NEEDS_JSON="$missing" "$root/scripts/ci-gate.sh" acceptance changes backend
expect_failure env NEEDS_JSON="$success" "$root/scripts/ci-gate.sh" acceptance changes

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"

if [ "$1" = api ]; then
  if [[ "$*" == *"/branches/"* ]]; then
    printf '%s\n' "$FAKE_REMOTE_SHA"
    exit 0
  fi
  if [[ "$*" == *"/issues?"* ]]; then
    if [ -s "$FAKE_GH_ISSUE" ]; then
      IFS='|' read -r number state title <"$FAKE_GH_ISSUE"
      state="$(printf '%s' "$state" | tr '[:upper:]' '[:lower:]')"
      jq -nc --argjson number "$number" --arg state "$state" --arg title "$title" \
        '{number: $number, state: $state, title: $title}'
    fi
    exit 0
  fi
  echo "unexpected gh api command: $*" >&2
  exit 2
fi

if [ "$1" != issue ]; then
  echo "unexpected gh command: $*" >&2
  exit 2
fi

case "$2" in
  create)
    title=""
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --title ]; then title="$2"; break; fi
      shift
    done
    printf '1|OPEN|%s\n' "$title" >"$FAKE_GH_ISSUE"
    printf 'https://github.com/example/attn/issues/1\n'
    ;;
  edit)
    ;;
  close)
    IFS='|' read -r number _ title <"$FAKE_GH_ISSUE"
    printf '%s|CLOSED|%s\n' "$number" "$title" >"$FAKE_GH_ISSUE"
    ;;
  reopen)
    IFS='|' read -r number _ title <"$FAKE_GH_ISSUE"
    printf '%s|OPEN|%s\n' "$number" "$title" >"$FAKE_GH_ISSUE"
    ;;
  *)
    echo "unexpected gh issue command: $*" >&2
    exit 2
    ;;
esac
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export FAKE_GH_LOG="$work/gh.log"
export FAKE_GH_ISSUE="$work/issue"
export GITHUB_REPOSITORY="example/attn"
export BRANCH_HEALTH_ASSIGNEE="victorarias"

sha_a="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
sha_b="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
export FAKE_REMOTE_SHA="$sha_a"
run_url="https://github.com/example/attn/actions/runs/1"

"$root/scripts/ci-branch-health.sh" failure next "$sha_a" "$run_url" >/dev/null
"$root/scripts/ci-branch-health.sh" failure next "$sha_a" "$run_url" >/dev/null
[ "$(grep -c '^issue create ' "$FAKE_GH_LOG")" -eq 1 ]
[ "$(grep -Ec '^issue (create|reopen|close) ' "$FAKE_GH_LOG")" -eq 1 ]

"$root/scripts/ci-branch-health.sh" success next "$sha_a" "$run_url" >/dev/null
"$root/scripts/ci-branch-health.sh" success next "$sha_a" "$run_url" >/dev/null
[ "$(grep -c '^issue close ' "$FAKE_GH_LOG")" -eq 1 ]

"$root/scripts/ci-branch-health.sh" failure next "$sha_a" "$run_url" >/dev/null
[ "$(grep -c '^issue create ' "$FAKE_GH_LOG")" -eq 1 ]
[ "$(grep -c '^issue reopen ' "$FAKE_GH_LOG")" -eq 1 ]

before="$(wc -l <"$FAKE_GH_LOG")"
export FAKE_REMOTE_SHA="$sha_b"
"$root/scripts/ci-branch-health.sh" failure next "$sha_a" "$run_url" >/dev/null
after="$(wc -l <"$FAKE_GH_LOG")"
[ "$after" -eq $((before + 1)) ]

echo "ci acceptance scripts: OK"
