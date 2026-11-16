#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/ci-branch-health.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-ci-branch-health-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"
case "$1 $2" in
  'api repos/example/attn/branches/main') echo "$FAKE_MAIN_SHA" ;;
  'api --paginate')
    jq -c '.[] | {number,state,title}' "$FAKE_ISSUES"
    ;;
  'issue create')
    title="$(sed -n 's/.*--title \(Branch health: main [A-Za-z ]*\) --body-file.*/\1/p' <<<"$*")"
    jq --arg title "$title" '. + [{number: (99 + length), state: "open", title: $title}]' "$FAKE_ISSUES" >"$FAKE_ISSUES.new"
    mv "$FAKE_ISSUES.new" "$FAKE_ISSUES"
    ;;
  'issue close')
    jq --argjson number "$3" 'map(if .number == $number then .state = "closed" else . end)' "$FAKE_ISSUES" >"$FAKE_ISSUES.new"
    mv "$FAKE_ISSUES.new" "$FAKE_ISSUES"
    ;;
  'issue reopen'|'issue edit') ;;
  *) echo "unexpected gh command: $*" >&2; exit 2 ;;
esac
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export FAKE_GH_LOG="$work/gh.log"
export FAKE_ISSUES="$work/issues.json"
head_sha=1111111111111111111111111111111111111111
nightly_sha=2222222222222222222222222222222222222222
export FAKE_MAIN_SHA="$head_sha"
run_url=https://github.com/example/attn/actions/runs/1

issue_state() {
  jq -r --arg title "$1" '.[] | select(.title == $title) | .state' "$FAKE_ISSUES"
}

echo '[]' >"$FAKE_ISSUES"
"$script" failure main "$nightly_sha" "$run_url" 'full run' >/dev/null
if [ "$(issue_state 'Branch health: main full run')" != open ]; then
  echo "a red full run on an older main commit did not open its issue" >&2
  cat "$FAKE_ISSUES" >&2
  exit 1
fi
if grep -q 'branches/main' "$FAKE_GH_LOG"; then
  echo "a full run's result was dropped because main moved" >&2
  exit 1
fi

"$script" success main "$head_sha" "$run_url" acceptance >/dev/null
if [ "$(issue_state 'Branch health: main full run')" != open ]; then
  echo "a green push closed the full run's issue" >&2
  exit 1
fi

"$script" success main "$head_sha" "$run_url" 'full run' >/dev/null
if [ "$(issue_state 'Branch health: main full run')" != closed ]; then
  echo "a green full run left its issue open" >&2
  exit 1
fi

"$script" failure main "$head_sha" "$run_url" macOS >/dev/null
if [ "$(issue_state 'Branch health: main macOS')" != open ]; then
  echo "a red macOS run did not open its issue" >&2
  cat "$FAKE_ISSUES" >&2
  exit 1
fi

"$script" success main "$head_sha" "$run_url" acceptance >/dev/null
if [ "$(issue_state 'Branch health: main macOS')" != open ]; then
  echo "a green Linux push closed the macOS issue" >&2
  exit 1
fi

export FAKE_MAIN_SHA="$nightly_sha"
"$script" success main "$head_sha" "$run_url" macOS >/dev/null
if [ "$(issue_state 'Branch health: main macOS')" != open ]; then
  echo "a green macOS run on a commit main moved past closed the macOS issue" >&2
  exit 1
fi
export FAKE_MAIN_SHA="$head_sha"

"$script" success main "$head_sha" "$run_url" macOS >/dev/null
if [ "$(issue_state 'Branch health: main macOS')" != closed ]; then
  echo "a green macOS run left its issue open" >&2
  exit 1
fi

if "$script" success main "$head_sha" "$run_url" nightly 2>/dev/null; then
  echo "an unknown record was accepted" >&2
  exit 1
fi

echo "ci-branch-health: ok"
