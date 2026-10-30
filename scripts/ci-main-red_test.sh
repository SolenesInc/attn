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
    view) run="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "${run:-}" ]; then
  jq -r "$jq_filter" "$FAKE_RUNS/$run.json"
else
  jq -r "$jq_filter" "$FAKE_RUNS/list.json"
fi
EOF
chmod +x "$work/bin/gh"

fixture() {
  local name="$1"
  mkdir -p "$work/$name"
  printf '%s' "$2" >"$work/$name/list.json"
  shift 2
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

fixture cancelled-with-failed-job '[{"databaseId":3},{"databaseId":2},{"databaseId":1}]' \
  3 '{"attempt":1,"conclusion":"cancelled","jobs":[]}' \
  2 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"success"},{"conclusion":"failure"},{"conclusion":"cancelled"}]}' \
  1 '{"attempt":1,"conclusion":"success","jobs":[]}'
expect cancelled-with-failed-job 0 "2 1"

fixture failed-rerun '[{"databaseId":7}]' \
  7 '{"attempt":2,"conclusion":"failure","jobs":[{"conclusion":"timed_out"}]}'
expect failed-rerun 0 "7 2"

fixture green-after-cancelled '[{"databaseId":5},{"databaseId":4}]' \
  5 '{"attempt":1,"conclusion":"cancelled","jobs":[{"conclusion":"success"},{"conclusion":"cancelled"}]}' \
  4 '{"attempt":1,"conclusion":"success","jobs":[{"conclusion":"success"}]}'
expect green-after-cancelled 0 ""

fixture undecided '[{"databaseId":6}]' \
  6 '{"attempt":1,"conclusion":"cancelled","jobs":[]}'
expect undecided 1 ""
grep -q "run_limit" "$work/undecided.err" || {
  echo "FAIL undecided: stderr does not name run_limit: $(cat "$work/undecided.err")" >&2
  exit 1
}

echo "ci-main-red: ok"
