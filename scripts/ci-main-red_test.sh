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
    */actions/runs/*/attempts/*/jobs\?*)
      run="${1#*/actions/runs/}"
      run="${run%%/*}"
      shift
      ;;
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
cat >"$work/bin/date" <<'EOF'
#!/usr/bin/env bash
echo "$FAKE_UTC_HOUR_MINUTE"
EOF
chmod +x "$work/bin/date"

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
  out="$(FAKE_UTC_HOUR_MINUTE="${now:-2026-11-23T22 17}" FAKE_RUNS="$work/$name" PATH="$work/bin:$PATH" "$script" 2>"$work/$name.err")" || status=$?
  if [ "$status" != "$want_status" ] || [ "$out" != "$want_out" ]; then
    echo "FAIL $name: status $status, output '$out', stderr '$(cat "$work/$name.err")'; want status $want_status, output '$want_out'" >&2
    exit 1
  fi
}

fixture cancelled-with-failed-job \
  ci.yml-push '[
    {"databaseId":3,"attempt":1,"conclusion":"cancelled"},
    {"databaseId":2,"attempt":1,"conclusion":"cancelled"},
    {"databaseId":1,"attempt":1,"conclusion":"success"}
  ]' \
  3 '{"attempt":1,"conclusion":"cancelled","jobs":[]}' \
  2 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"success"},{"conclusion":"failure"},{"conclusion":"cancelled"}]}' \
  1 '{"attempt":1,"conclusion":"success","jobs":[]}'
expect cancelled-with-failed-job 0 "2 1
window 2026-11-23T22:00Z"

fixture failed-rerun ci.yml-push '[{"databaseId":7,"attempt":2,"conclusion":"failure"}]' \
  7 '{"attempt":2,"conclusion":"failure","jobs":[{"conclusion":"timed_out"}]}'
expect failed-rerun 0 "7 2
window 2026-11-23T22:00Z"
now="2026-11-23T22 29" expect failed-rerun 0 "7 2
window 2026-11-23T22:00Z"
now="2026-11-23T22 30" expect failed-rerun 0 "7 2
window 2026-11-23T22:30Z"
now="2026-11-23T23 05" expect failed-rerun 0 "7 2
window 2026-11-23T23:00Z"

fixture green-after-cancelled ci.yml-push '[{"databaseId":5,"attempt":1,"conclusion":"cancelled"},{"databaseId":4,"attempt":1,"conclusion":"success"}]' \
  5 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"success"},{"conclusion":"cancelled"}]}' \
  4 '{"attempt":1,"conclusion":"success","jobs":[{"conclusion":"success"}]}'
expect green-after-cancelled 0 ""
now="2026-11-23T22 30" expect green-after-cancelled 0 ""

fixture red-macos-push-beside-green-linux \
  ci.yml-push '[{"databaseId":21,"attempt":1,"conclusion":"success"}]' \
  ci-macos.yml-push '[{"databaseId":22,"attempt":1,"conclusion":"failure"}]' \
  21 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  22 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}'
expect red-macos-push-beside-green-linux 0 "22 1
window 2026-11-23T22:00Z"

fixture red-nightly-behind-green-pushes \
  ci.yml-push '[{"databaseId":9,"attempt":1,"conclusion":"success"}]' \
  ci-macos.yml-push '[{"databaseId":13,"attempt":1,"conclusion":"success"}]' \
  ci-macos.yml-all '[
    {"databaseId":13,"event":"push","attempt":1,"conclusion":"success"},
    {"databaseId":8,"event":"schedule","attempt":1,"conclusion":"failure"}
  ]' \
  9 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  13 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  8 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"success"},{"conclusion":"failure"}]}'
expect red-nightly-behind-green-pushes 0 "8 1
window 2026-11-23T22:00Z"

fixture red-ci-nightly-behind-green-pushes \
  ci.yml-push '[{"databaseId":31,"attempt":1,"conclusion":"success"}]' \
  ci.yml-all '[
    {"databaseId":31,"event":"push","attempt":1,"conclusion":"success"},
    {"databaseId":30,"event":"workflow_dispatch","attempt":1,"conclusion":"failure"}
  ]' \
  31 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  30 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}'
expect red-ci-nightly-behind-green-pushes 0 "30 1
window 2026-11-23T22:00Z"

fixture red-push-and-green-nightly \
  ci.yml-push '[{"databaseId":11,"attempt":1,"conclusion":"failure"}]' \
  ci-macos.yml-all '[
    {"databaseId":12,"event":"workflow_dispatch","attempt":1,"conclusion":"success"},
    {"databaseId":10,"event":"schedule","attempt":1,"conclusion":"success"}
  ]' \
  12 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  11 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}' \
  10 '{"attempt":1,"conclusion":"success","jobs":[]}'
expect red-push-and-green-nightly 0 "11 1
window 2026-11-23T22:00Z"

fixture red-in-every-stream \
  ci.yml-push '[{"databaseId":31,"attempt":1,"conclusion":"failure"}]' \
  ci-macos.yml-push '[{"databaseId":32,"attempt":1,"conclusion":"failure"}]' \
  ci-macos.yml-all '[{"databaseId":33,"event":"schedule","attempt":1,"conclusion":"failure"}]' \
  31 '{"attempt":1,"conclusion":"failure","jobs":[]}' \
  32 '{"attempt":1,"conclusion":"failure","jobs":[]}' \
  33 '{"attempt":1,"conclusion":"failure","jobs":[]}'
expect red-in-every-stream 0 "31 1
32 1
33 1
window 2026-11-23T22:00Z"

fixture recovered-in-every-stream \
  ci.yml-push '[{"databaseId":103,"attempt":1,"conclusion":"success"},{"databaseId":101,"attempt":2,"conclusion":"failure"}]' \
  ci.yml-all '[
    {"databaseId":103,"event":"push","attempt":1,"conclusion":"success"},
    {"databaseId":202,"event":"workflow_dispatch","attempt":1,"conclusion":"success"},
    {"databaseId":201,"event":"schedule","attempt":1,"conclusion":"cancelled"},
    {"databaseId":200,"event":"schedule","attempt":1,"conclusion":"cancelled"}
  ]' \
  ci-macos.yml-push '[{"databaseId":302,"attempt":1,"conclusion":"success"},{"databaseId":301,"attempt":1,"conclusion":"failure"}]' \
  ci-macos.yml-all '[
    {"databaseId":402,"event":"schedule","attempt":1,"conclusion":"success"},
    {"databaseId":401,"event":"workflow_dispatch","attempt":1,"conclusion":"timed_out"}
  ]' \
  103 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  101 '{"attempt":2,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}' \
  202 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  201 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"timed_out"},{"conclusion":"cancelled"}]}' \
  200 '{"attempt":1,"conclusion":"cancelled","jobs":[]}' \
  302 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  301 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}' \
  402 '{"attempt":1,"conclusion":"success","jobs":[]}' \
  401 '{"attempt":1,"conclusion":"timed_out","jobs":[{"conclusion":"timed_out"}]}'
recovered="recovered 101 2
recovered 201 1
recovered 301 1
recovered 401 1"
expect recovered-in-every-stream 0 "$recovered"

fixture recovered-in-every-stream \
  ci.yml-push '[
    {"databaseId":104,"attempt":1,"conclusion":"success"},
    {"databaseId":103,"attempt":1,"conclusion":"success"},
    {"databaseId":101,"attempt":2,"conclusion":"failure"}
  ]' \
  104 '{"attempt":1,"conclusion":"success","jobs":[]}'
now="2026-11-23T22 30" expect recovered-in-every-stream 0 "$recovered"

fixture recovered-in-every-stream \
  ci.yml-push '[
    {"databaseId":105,"attempt":1,"conclusion":"failure"},
    {"databaseId":104,"attempt":1,"conclusion":"success"},
    {"databaseId":101,"attempt":2,"conclusion":"failure"}
  ]' \
  105 '{"attempt":1,"conclusion":"failure","jobs":[{"conclusion":"failure"}]}'
expect recovered-in-every-stream 0 "105 1
$recovered
window 2026-11-23T22:00Z"

fixture historical-jobs-unavailable \
  ci.yml-push '[
    {"databaseId":113,"attempt":1,"conclusion":"failure"},
    {"databaseId":112,"attempt":1,"conclusion":"success"},
    {"databaseId":111,"attempt":1,"conclusion":"cancelled"},
    {"databaseId":110,"attempt":1,"conclusion":"failure"}
  ]'
expect historical-jobs-unavailable 0 "113 1
recovered 110 1
window 2026-11-23T22:00Z"

fixture undecided ci.yml-push '[{"databaseId":6,"attempt":1,"conclusion":"cancelled"}]' \
  6 '{"attempt":1,"conclusion":"cancelled","jobs":[]}'
expect undecided 1 ""
grep -q "run_limit" "$work/undecided.err" || {
  echo "FAIL undecided: stderr does not name run_limit: $(cat "$work/undecided.err")" >&2
  exit 1
}

echo "ci-main-red: ok"
