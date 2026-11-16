#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/ci-acceptance.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-ci-acceptance-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GH_LOG"
[[ "$*" == *'/actions/runs/7/attempts/2/jobs?'* ]] || { echo "unexpected gh command: $*" >&2; exit 2; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --jq) jq_filter="$2"; shift 2 ;;
    *) shift ;;
  esac
done
jq -c "$jq_filter" "$FAKE_JOBS"
EOF
chmod +x "$work/bin/gh"

export PATH="$work/bin:$PATH"
export GITHUB_REPOSITORY=example/attn
export GITHUB_RUN_ID=7
export GITHUB_RUN_ATTEMPT=2
export GITHUB_SHA=1111111111111111111111111111111111111111
export FAKE_GH_LOG="$work/gh.log"
export FAKE_JOBS="$work/jobs.json"

ok='{"result":"success"}'
skipped='{"result":"skipped"}'
failed='{"result":"failure"}'
cancelled='{"result":"cancelled"}'
acceptance='{"name":"Acceptance","status":"in_progress","conclusion":null,"runner_name":"GitHub Actions 3","labels":["ubuntu-24.04"]}'
linux_green='{"name":"PTY upgrade (ubuntu-24.04)","status":"completed","conclusion":"success","runner_name":"GitHub Actions 1","labels":["ubuntu-24.04"]}'
macos_never_started='{"name":"PTY upgrade (macos-15)","status":"completed","conclusion":"cancelled","runner_name":null,"labels":["macos-15"]}'
macos_aa_never_started='{"name":"App acceptance macOS","status":"completed","conclusion":"cancelled","runner_name":"","labels":["macos-15"]}'
macos_failed='{"name":"PTY upgrade (macos-15)","status":"completed","conclusion":"failure","runner_name":"GitHub Actions 2","labels":["macos-15"]}'
macos_cancelled_while_running='{"name":"PTY upgrade (macos-15)","status":"completed","conclusion":"cancelled","runner_name":"GitHub Actions 2","labels":["macos-15"]}'
linux_never_started='{"name":"Frontend","status":"completed","conclusion":"cancelled","runner_name":null,"labels":["ubuntu-24.04-arm"]}'
full_macos_failed='{"name":"Full macOS run","status":"completed","conclusion":"failure","runner_name":"GitHub Actions 4","labels":["ubuntu-24.04"]}'

expect() {
  local name="$1" want_status="$2" needs="$3" jobs="$4" status=0
  printf '%s' "$jobs" >"$FAKE_JOBS"
  : >"$FAKE_GH_LOG"
  NEEDS_JSON="$needs" GITHUB_STEP_SUMMARY="$work/$name.summary" "$script" >"$work/$name.out" 2>"$work/$name.err" || status=$?
  if [ "$status" != "$want_status" ]; then
    echo "FAIL $name: status $status, want $want_status" >&2
    cat "$work/$name.out" "$work/$name.err" >&2
    exit 1
  fi
}

expect all-green 0 "{\"backend\":$ok,\"pty-compatibility\":$ok,\"app-acceptance-macos\":$ok,\"full-macos\":$ok}" '{"jobs":[]}'
if [ -s "$FAKE_GH_LOG" ]; then
  echo "FAIL all-green: a green run read the jobs API" >&2
  exit 1
fi

expect gated-macos-skipped 0 "{\"backend\":$ok,\"app-acceptance-macos\":$skipped,\"full-macos\":$skipped}" '{"jobs":[]}'

expect linux-skipped 1 "{\"backend\":$skipped,\"full-macos\":$skipped}" '{"jobs":[]}'

expect macos-never-started 0 "{\"backend\":$ok,\"pty-compatibility\":$cancelled,\"app-acceptance-macos\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$linux_green,$macos_never_started,$macos_aa_never_started]}"
grep -Fq '::warning title=macOS coverage missing::No runner picked up PTY upgrade (macos-15), App acceptance macOS, so 1111111111111111111111111111111111111111 has no macOS coverage' "$work/macos-never-started.out"
grep -Fq 'macOS coverage missing' "$work/macos-never-started.summary"

expect macos-failed 1 "{\"backend\":$ok,\"pty-compatibility\":$failed,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$linux_green,$macos_failed]}"

expect macos-cancelled-while-running 1 "{\"backend\":$ok,\"pty-compatibility\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$linux_green,$macos_cancelled_while_running]}"

expect linux-never-started 1 "{\"frontend\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$linux_never_started]}"

expect never-started-beside-a-red-job 1 "{\"backend\":$failed,\"pty-compatibility\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$macos_never_started,{\"name\":\"Daemon (1)\",\"status\":\"completed\",\"conclusion\":\"failure\",\"runner_name\":\"GitHub Actions 5\",\"labels\":[\"ubuntu-24.04\"]}]}"

expect full-run-macos-never-started 1 "{\"backend\":$ok,\"pty-compatibility\":$cancelled,\"full-macos\":$failed}" \
  "{\"jobs\":[$acceptance,$linux_green,$macos_never_started,$full_macos_failed]}"

echo "ci-acceptance: ok"
