#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/ci-main-red.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-ci-main-red-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
jq_filter=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --jq) jq_filter="$2"; shift 2 ;;
    --event) event="$2"; shift 2 ;;
    --workflow) workflow="$2"; shift 2 ;;
    view) run="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "${run:-}" ]; then
  jq -r "$jq_filter" "$FAKE_RUNS/$run.json"
elif [ -f "$FAKE_RUNS/$workflow-${event:-all}.json" ]; then
  jq -r "$jq_filter" "$FAKE_RUNS/$workflow-${event:-all}.json"
fi
EOF
chmod +x "$work/bin/gh"

fixture() {
  local name="$1"
  shift
  mkdir -p "$work/$name"
  while [ "$#" -gt 0 ]; do
    printf '%s' "$2" >"$work/$name/$1.json"
    shift 2
  done
}

expect() {
  local name="$1" want_status="$2" want_out="$3" status=0 out
  out="$(FAKE_RUNS="$work/$name" PATH="$work/bin:$PATH" "$script" 2>"$work/$name.err")" || status=$?
  if [ "$status" != "$want_status" ] || [ "$out" != "$want_out" ]; then
    echo "FAIL $name: status $status, output '$out', stderr '$(cat "$work/$name.err")'; want status $want_status, output '$want_out'" >&2
    exit 1
  fi
}

fixture cancelled-with-failed-job \
  ci.yml-push '[{"databaseId":3},{"databaseId":2},{"databaseId":1}]' \
  3 '{"attempt":1,"conclusion":"cancelled","jobs":[]}' \
  2 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"success"},{"conclusion":"failure"},{"conclusion":"cancelled"}]}' \
  1 '{"attempt":1,"conclusion":"success","jobs":[]}'
expect cancelled-with-failed-job 0 "2 1"

fixture failed-rerun ci.yml-push '[{"databaseId":7}]' \
  7 '{"attempt":2,"conclusion":"failure","jobs":[{"conclusion":"timed_out"}]}'
expect failed-rerun 0 "7 2"

fixture green-after-cancelled ci.yml-push '[{"databaseId":5},{"databaseId":4}]' \
  5 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"success"},{"conclusion":"cancelled"}]}' \
  4 '{"attempt":1,"conclusion":"success","jobs":[{"conclusion":"success"}]}'
expect green-after-cancelled 0 ""

fixture red-macos-push-beside-green-linux \
  ci.yml-push '[{"databaseId":21}]' \
  ci-macos.yml-push '[{"databaseId":22}]' \
  21 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  22 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}'
expect red-macos-push-beside-green-linux 0 "22 1"

fixture red-nightly-behind-green-pushes \
  ci.yml-push '[{"databaseId":9}]' \
  ci-macos.yml-push '[{"databaseId":13}]' \
  ci-macos.yml-all '[{"databaseId":13,"event":"push"},{"databaseId":8,"event":"schedule"}]' \
  9 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  13 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  8 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"success"},{"conclusion":"failure"}]}'
expect red-nightly-behind-green-pushes 0 "8 1"

fixture red-push-and-green-nightly \
  ci.yml-push '[{"databaseId":11}]' \
  ci-macos.yml-all '[{"databaseId":12,"event":"workflow_dispatch"},{"databaseId":10,"event":"schedule"}]' \
  12 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  11 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}' \
  10 '{"attempt":1,"conclusion":"success","jobs":[]}'
expect red-push-and-green-nightly 0 "11 1"

fixture red-in-every-stream \
  ci.yml-push '[{"databaseId":31}]' \
  ci-macos.yml-push '[{"databaseId":32}]' \
  ci-macos.yml-all '[{"databaseId":33,"event":"schedule"}]' \
  31 '{"attempt":1,"conclusion":"failure","jobs":[]}' \
  32 '{"attempt":1,"conclusion":"failure","jobs":[]}' \
  33 '{"attempt":1,"conclusion":"failure","jobs":[]}'
expect red-in-every-stream 0 "31 1
32 1
33 1"

fixture undecided ci.yml-push '[{"databaseId":6}]' \
  6 '{"attempt":1,"conclusion":"cancelled","jobs":[]}'
expect undecided 1 ""
grep -q "run_limit" "$work/undecided.err" || {
  echo "FAIL undecided: stderr does not name run_limit: $(cat "$work/undecided.err")" >&2
  exit 1
}

echo "ci-main-red: ok"
