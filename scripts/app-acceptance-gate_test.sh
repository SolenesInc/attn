#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gate="$root/scripts/app-acceptance-gate.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-app-acceptance-gate-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/pulls/42/commits?'* ]]; then
  printf '%s\n' "$FAKE_APP_SHA"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/pulls'* ]]; then
  printf '%s\n' "${FAKE_CANDIDATE_ROWS:-}"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *"/git/commits/$FAKE_APP_SHA"* ]]; then
  printf '%s\n' "$FAKE_APP_TREE"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *"/git/commits/$FAKE_MAIN_SHA"* ]]; then
  printf '%s\n' "$FAKE_MAIN_TREE"
  exit 0
fi
if [[ "$1" == api ]] && [[ "$*" == *'/actions/workflows/app-acceptance.yml/runs?'* ]]; then
  if [[ "${FAKE_APP_MODE:-success}" != missing ]]; then
    printf '43\tApp acceptance %s\tcompleted\tsuccess\t%s\n' "$FAKE_APP_SHA" \
      'https://github.com/example/attn/actions/runs/43'
  fi
  exit 0
fi
if [[ "$1 $2" == "api --paginate" ]] && [[ "$*" == *'/actions/runs/43/jobs?'* ]]; then
  printf '%s\t%s\t%s\n' "${FAKE_APP_STATUS:-completed}" \
    "${FAKE_APP_CONCLUSION:-success}" \
    'https://github.com/example/attn/actions/runs/43/job/8'
  exit 0
fi
echo "unexpected gh command: $*" >&2
exit 2
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
main_sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
candidate_sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
export FAKE_MAIN_SHA="$main_sha"
export FAKE_APP_SHA="$candidate_sha"
export FAKE_APP_TREE=cccccccccccccccccccccccccccccccccccccccc
export FAKE_MAIN_TREE="$FAKE_APP_TREE"
export FAKE_APP_STATUS=completed
export FAKE_APP_CONCLUSION=success
export FAKE_APP_MODE=success
export FAKE_CANDIDATE_ROWS=$'42\trelease/v1.2.3\thttps://github.com/example/attn/pull/42'

expect_failure() {
  local expected="$1"
  shift
  if "$@" >"$work/failure.out" 2>&1; then
    echo "expected app acceptance gate failure: $*" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$work/failure.out"; then
    echo "failure did not contain '$expected':" >&2
    cat "$work/failure.out" >&2
    exit 1
  fi
}

"$gate" "$main_sha" >"$work/success.out"
grep -Fq 'PR #42 release/v1.2.3 passed' "$work/success.out"

export FAKE_MAIN_TREE=dddddddddddddddddddddddddddddddddddddddd
expect_failure 'differs from app-accepted candidate tree' "$gate" "$main_sha"
export FAKE_MAIN_TREE="$FAKE_APP_TREE"

export FAKE_APP_CONCLUSION=failure
expect_failure 'App acceptance is completed/failure' "$gate" "$main_sha"
export FAKE_APP_CONCLUSION=success

export FAKE_APP_MODE=missing
expect_failure 'has no app-acceptance.yml workflow_dispatch run' "$gate" "$main_sha"
export FAKE_APP_MODE=success

export FAKE_CANDIDATE_ROWS=''
expect_failure 'has 0 associated release candidate PRs' "$gate" "$main_sha"

echo "app acceptance gate: OK"
