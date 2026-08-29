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
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  if [[ "${FAKE_RUN_MODE:-success}" != missing ]]; then
    printf '42\tApp acceptance %s\t%s\t%s\t%s\n' "$FAKE_RUN_SHA" \
      "${FAKE_RUN_STATUS:-completed}" "${FAKE_RUN_CONCLUSION:-success}" \
      'https://github.com/example/attn/actions/runs/42'
  fi
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/'*'/runs?'* ]]; then
  if [[ "${FAKE_RUN_MODE:-success}" != missing ]]; then
    printf '42\t%s\t%s\t%s\t%s\n' "$FAKE_RUN_SHA" \
      "${FAKE_RUN_STATUS:-completed}" "${FAKE_RUN_CONCLUSION:-success}" \
      'https://github.com/example/attn/actions/runs/42'
  fi
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/42/jobs?'* ]]; then
  if [[ "${FAKE_JOB_MODE:-success}" != missing ]]; then
    printf '%s\t%s\t%s\n' "${FAKE_JOB_STATUS:-completed}" \
      "${FAKE_JOB_CONCLUSION:-success}" \
      'https://github.com/example/attn/actions/runs/42/job/7'
  fi
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
grep -Fq 'actions/workflows/ci.yml/runs?event=push' "$FAKE_GH_LOG"
grep -Fq 'head_sha=' "$FAKE_GH_LOG"
grep -Fq 'event=push' "$FAKE_GH_LOG"
grep -Fq 'branch=main' "$FAKE_GH_LOG"

: >"$FAKE_GH_LOG"
"$gate" app-acceptance.yml "$sha" workflow_dispatch main 'App acceptance' \
  >"$work/app-success.out"
grep -Fq 'app-acceptance.yml run 42 and App acceptance are green' "$work/app-success.out"
grep -Fq 'actions/workflows/app-acceptance.yml/runs?event=workflow_dispatch&per_page=100&branch=main' "$FAKE_GH_LOG"
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

export FAKE_RUN_MODE=missing
expect_failure 'has no app-acceptance.yml workflow_dispatch run' \
  "$gate" app-acceptance.yml "$sha" workflow_dispatch main 'App acceptance'

echo "workflow job gate: OK"
