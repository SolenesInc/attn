#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/workflow-job-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-workflow-job-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"
filter=''
previous=''
for arg in "$@"; do
  if [[ "$previous" == --jq ]]; then
    filter="$arg"
  fi
  previous="$arg"
done
run_conclusion="${FAKE_RUN_CONCLUSION:-success}"
job_conclusion="${FAKE_JOB_CONCLUSION:-success}"
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  jq -n --arg mode "${FAKE_RUN_MODE:-success}" --arg sha "$FAKE_RUN_SHA" \
    --arg status "${FAKE_RUN_STATUS:-completed}" --arg conclusion "$run_conclusion" '{workflow_runs: (
      if $mode == "missing" then [] else [
        {created_at: "2026-08-29T10:00:00Z", id: 41, head_branch: "main", event: "workflow_dispatch",
         display_title: "App acceptance bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", status: "completed",
         conclusion: "success", html_url: "https://github.com/example/attn/actions/runs/41"},
        {created_at: "2026-08-29T10:00:00Z", id: 42, head_branch: "main", event: "workflow_dispatch",
         display_title: ("App acceptance " + $sha), status: $status,
         conclusion: (if $conclusion == "none" then null else $conclusion end),
         html_url: "https://github.com/example/attn/actions/runs/42"}
      ] end)}' | jq -r "$filter"
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/workflows/'*'/runs?'* ]]; then
  event="$(sed -E 's/.*[?&]event=([a-z_]+).*/\1/' <<<"$*")"
  jq -n --arg mode "${FAKE_RUN_MODE:-success}" --arg sha "$FAKE_RUN_SHA" --arg event "$event" \
    --arg status "${FAKE_RUN_STATUS:-completed}" --arg conclusion "$run_conclusion" '{workflow_runs: (
      if $mode == "missing" then [] else [
        {created_at: "2026-08-29T10:00:00Z", id: 42, head_sha: $sha, event: $event, status: $status,
         conclusion: (if $conclusion == "none" then null else $conclusion end),
         html_url: "https://github.com/example/attn/actions/runs/42"}
      ] end)}' | jq -r "$filter"
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/42/jobs?'* ]]; then
  jq -n --arg mode "${FAKE_JOB_MODE:-success}" --arg status "${FAKE_JOB_STATUS:-completed}" \
    --arg conclusion "$job_conclusion" '{jobs: (
      [{name: "App acceptance build", status: "completed", conclusion: "success",
        html_url: "https://github.com/example/attn/actions/runs/42/job/6"}] +
      if $mode == "missing" then [] else [
        {name: "Acceptance", status: $status, conclusion: $conclusion,
         html_url: "https://github.com/example/attn/actions/runs/42/job/7"},
        {name: "App acceptance", status: $status, conclusion: $conclusion,
         html_url: "https://github.com/example/attn/actions/runs/42/job/8"}
      ] end)}' | jq -r "$filter"
  exit 0
fi
echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export FAKE_GH_LOG="$work/gh.log"
sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export FAKE_RUN_SHA="$sha"
export FAKE_RUN_STATUS=completed
export FAKE_RUN_CONCLUSION=success
export FAKE_RUN_MODE=success
export FAKE_JOB_STATUS=completed
export FAKE_JOB_CONCLUSION=success
export FAKE_JOB_MODE=success

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected workflow job gate failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

"$gate" ci.yml "$sha" push main Acceptance >"$work/success.out"
grep -Fq 'ci.yml run 42 and Acceptance are green' "$work/success.out"
grep -Fq 'api --paginate repos/example/attn/actions/workflows/ci.yml/runs?event=push' "$FAKE_GH_LOG"
grep -Fq 'head_sha=' "$FAKE_GH_LOG"
grep -Fq 'event=push' "$FAKE_GH_LOG"
grep -Fq 'branch=main' "$FAKE_GH_LOG"

: >"$FAKE_GH_LOG"
"$gate" ci.yml "$sha" pull_request - 'App acceptance' >"$work/pr-success.out"
grep -Fq 'ci.yml run 42 and App acceptance are green' "$work/pr-success.out"
grep -Fq 'event=pull_request' "$FAKE_GH_LOG"
if grep -Fq 'branch=' "$FAKE_GH_LOG"; then
  echo "pull-request App acceptance gate added a branch filter" >&2
  exit 1
fi

export FAKE_RUN_STATUS=in_progress
export FAKE_RUN_CONCLUSION=none
"$gate" ci.yml "$sha" pull_request - 'App acceptance' >"$work/current-pr-success.out"
expect_failure 'ci.yml run is in_progress/none' \
  "$gate" ci.yml "$sha" push main Acceptance
export FAKE_RUN_STATUS=completed
export FAKE_RUN_CONCLUSION=success

: >"$FAKE_GH_LOG"
"$gate" app-acceptance.yml "$sha" workflow_dispatch main 'App acceptance' \
  >"$work/app-success.out"
grep -Fq 'app-acceptance.yml run 42 and App acceptance are green' "$work/app-success.out"
grep -Fq 'api --paginate repos/example/attn/actions/workflows/app-acceptance.yml/runs?event=workflow_dispatch&per_page=100&branch=main' "$FAKE_GH_LOG"
if grep -Fq 'head_sha=' "$FAKE_GH_LOG"; then
  echo "App acceptance gate trusted a candidate-head workflow" >&2
  exit 1
fi

export FAKE_RUN_CONCLUSION=failure
expect_failure 'ci.yml run is completed/failure' \
  "$gate" ci.yml "$sha" push main Acceptance
export FAKE_RUN_CONCLUSION=success

export FAKE_JOB_CONCLUSION=failure
expect_failure 'Acceptance is completed/failure' \
  "$gate" ci.yml "$sha" push main Acceptance
export FAKE_JOB_CONCLUSION=success

export FAKE_JOB_MODE=missing
expect_failure "has 0 'App acceptance' jobs" \
  "$gate" ci.yml "$sha" pull_request - 'App acceptance'
export FAKE_JOB_MODE=success

export FAKE_RUN_MODE=missing
expect_failure 'has no app-acceptance.yml workflow_dispatch run' \
  "$gate" app-acceptance.yml "$sha" workflow_dispatch main 'App acceptance'

echo "workflow job gate: OK"
