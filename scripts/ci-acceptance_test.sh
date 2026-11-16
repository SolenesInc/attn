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
abandoned='{"result":"abandoned"}'
acceptance='{"name":"macOS acceptance","status":"in_progress","conclusion":null,"runner_name":"GitHub Actions 3","labels":["ubuntu-24.04"]}'
macos_never_started='{"name":"PTY upgrade / macos-15","status":"completed","conclusion":"cancelled","runner_name":null,"labels":["macos-15"]}'
macos_aa_never_started='{"name":"App acceptance macOS","status":"completed","conclusion":"cancelled","runner_name":"","labels":["macos-15"]}'
macos_failed='{"name":"PTY upgrade / macos-15","status":"completed","conclusion":"failure","runner_name":"GitHub Actions 2","labels":["macos-15"]}'
macos_cancelled_while_running='{"name":"PTY upgrade / macos-15","status":"completed","conclusion":"cancelled","runner_name":"GitHub Actions 2","labels":["macos-15"]}'
linux_never_started='{"name":"Frontend","status":"completed","conclusion":"cancelled","runner_name":null,"labels":["ubuntu-24.04-arm"]}'
full_macos_failed='{"name":"Full macOS run","status":"completed","conclusion":"failure","runner_name":"GitHub Actions 4","labels":["ubuntu-24.04"]}'
macos_jobs=(pty-compatibility rust native attn-cli app-acceptance-macos full-macos)

expect() {
  local name="$1" want_status="$2" needs="$3" jobs="$4" status=0
  shift 4
  printf '%s' "$jobs" >"$FAKE_JOBS"
  : >"$FAKE_GH_LOG"
  NEEDS_JSON="$needs" GITHUB_STEP_SUMMARY="$work/$name.summary" "$script" "$@" >"$work/$name.out" 2>"$work/$name.err" || status=$?
  if [ "$status" != "$want_status" ]; then
    echo "FAIL $name: status $status, want $want_status" >&2
    cat "$work/$name.out" "$work/$name.err" >&2
    exit 1
  fi
}

expect linux-green 0 "{\"backend\":$ok,\"frontend\":$ok}" '{"jobs":[]}'
if [ -s "$FAKE_GH_LOG" ]; then
  echo "FAIL linux-green: a green run read the jobs API" >&2
  exit 1
fi

expect linux-skipped 1 "{\"backend\":$skipped,\"frontend\":$ok}" '{"jobs":[]}'

expect linux-never-started 1 "{\"frontend\":$cancelled}" \
  "{\"jobs\":[$acceptance,$linux_never_started]}"

expect macos-green 0 "{\"changes\":$ok,\"pty-compatibility\":$ok,\"app-acceptance-macos\":$ok,\"full-macos\":$ok}" '{"jobs":[]}' "${macos_jobs[@]}"

expect gated-macos-skipped 0 "{\"changes\":$ok,\"pty-compatibility\":$skipped,\"native\":$ok,\"app-acceptance-macos\":$skipped,\"full-macos\":$skipped}" '{"jobs":[]}' "${macos_jobs[@]}"

expect macos-changes-skipped 1 "{\"changes\":$skipped,\"pty-compatibility\":$skipped}" '{"jobs":[]}' "${macos_jobs[@]}"

expect macos-never-started 0 "{\"changes\":$ok,\"pty-compatibility\":$cancelled,\"app-acceptance-macos\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$macos_never_started,$macos_aa_never_started]}" "${macos_jobs[@]}"
grep -Fq '::warning title=macOS coverage missing::No runner picked up PTY upgrade / macos-15, App acceptance macOS, so 1111111111111111111111111111111111111111 has no macOS coverage' "$work/macos-never-started.out"
grep -Fq 'macOS coverage missing' "$work/macos-never-started.summary"

expect macos-never-started-reported-abandoned 0 "{\"changes\":$ok,\"attn-cli\":$abandoned,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,{\"name\":\"CLI / macos-15\",\"status\":\"completed\",\"conclusion\":\"cancelled\",\"runner_name\":\"\",\"labels\":[\"macos-15\"]}]}" "${macos_jobs[@]}"

expect macos-failed-reported-abandoned 1 "{\"changes\":$ok,\"pty-compatibility\":$abandoned,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$macos_failed]}" "${macos_jobs[@]}"

expect macos-failed 1 "{\"changes\":$ok,\"pty-compatibility\":$failed,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$macos_failed]}" "${macos_jobs[@]}"

expect macos-cancelled-while-running 1 "{\"changes\":$ok,\"pty-compatibility\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$macos_cancelled_while_running]}" "${macos_jobs[@]}"

expect never-started-beside-a-red-job 1 "{\"changes\":$ok,\"native\":$failed,\"pty-compatibility\":$cancelled,\"full-macos\":$skipped}" \
  "{\"jobs\":[$acceptance,$macos_never_started,{\"name\":\"Native / macos-15\",\"status\":\"completed\",\"conclusion\":\"failure\",\"runner_name\":\"GitHub Actions 5\",\"labels\":[\"macos-15\"]}]}" "${macos_jobs[@]}"

expect full-run-macos-never-started 1 "{\"changes\":$ok,\"pty-compatibility\":$cancelled,\"full-macos\":$failed}" \
  "{\"jobs\":[$acceptance,$macos_never_started,$full_macos_failed]}" "${macos_jobs[@]}"

grep -Fxq '        run: ./scripts/ci-acceptance.sh' "$root/.github/workflows/ci.yml"
grep -Fq "run: ./scripts/ci-acceptance.sh ${macos_jobs[*]}" "$root/.github/workflows/ci-macos.yml"

echo "ci-acceptance: ok"
