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
if ! grep -Fq "              - 'scripts/**'" "$workflow"; then
  echo "Daemon path filter must cover every repository script" >&2
  exit 1
fi

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

success='{"changes":{"result":"success"},"backend":{"result":"success"}}'
filtered='{"changes":{"result":"success"},"backend":{"result":"skipped"}}'
failed='{"changes":{"result":"success"},"backend":{"result":"failure"}}'
missing='{"changes":{"result":"success"}}'

NEEDS_JSON="$success" "$root/scripts/ci-gate.sh" acceptance changes backend >/dev/null
NEEDS_JSON="$filtered" "$root/scripts/ci-gate.sh" pr changes backend >/dev/null
expect_failure env NEEDS_JSON="$filtered" "$root/scripts/ci-gate.sh" acceptance changes backend
expect_failure env NEEDS_JSON="$failed" "$root/scripts/ci-gate.sh" pr changes backend
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
