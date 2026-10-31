#!/usr/bin/env bash
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(git -C "$here" rev-parse --show-toplevel)"
vitest="$root/app/node_modules/.bin/vitest"
playwright="$root/app/node_modules/.bin/playwright"
[ -x "$vitest" ] && [ -x "$playwright" ] || { echo "run make app/node_modules/.attn-installed first" >&2; exit 2; }
work=/tmp/attn-test-history-fixtures
rm -rf "$work"
mkdir -p "$work"
cp -R "$here/projects/." "$work/"
for project in playwright playwright-slow playwright-broken; do ln -s "$root/app/node_modules" "$work/$project/node_modules"; done

interrupt_when() {
  local file="$1" pattern="$2" pid="$3"
  until grep -q "$pattern" "$file" 2>/dev/null; do sleep 0.1; done
  kill -INT -- "-$pid"
  wait "$pid"
}

set -m
cd "$work/go" || exit 1
go test -json -count=1 ./... >"$here/go/mixed.jsonl"
go test -json -run 'TestPasses|TestSkips' ./good >/dev/null
go test -json -run 'TestPasses|TestSkips' ./good >"$here/go/cached.jsonl"
FIXTURE_SLEEP=1 go test -json -count=1 -timeout 2s -run 'TestPasses|TestSleeps' ./good >"$here/go/timeout.jsonl"
FIXTURE_SLEEP=1 go test -json -count=1 -run 'TestPasses|TestSleeps' ./good >"$here/go/interrupted.jsonl" &
interrupt_when "$here/go/interrupted.jsonl" '"Action":"run".*"Test":"TestSleeps"' "$!"
head -c 4000 "$here/go/mixed.jsonl" >"$here/go/truncated.jsonl"

cd "$work/vitest" || exit 1
rm -f "$here/vitest/mixed.jsonl"
ATTN_TEST_RESULTS="$here/vitest/mixed.jsonl" "$vitest" run --config vitest.config.mjs \
  --reporter=default --reporter="$root/app/scripts/vitest-results-reporter.mjs" >/dev/null 2>&1
cd "$work/vitest-slow" || exit 1
rm -f "$here/vitest/interrupted.jsonl"
ATTN_TEST_RESULTS="$here/vitest/interrupted.jsonl" "$vitest" run --no-file-parallelism --config vitest.config.mjs \
  --reporter=default --reporter="$root/app/scripts/vitest-results-reporter.mjs" >/dev/null 2>&1 &
interrupt_when "$here/vitest/interrupted.jsonl" 'finishes first' "$!"

for run in playwright:mixed playwright-broken:load-error playwright-slow:interrupted; do
  project="${run%%:*}" name="${run#*:}"
  cd "$work/$project" || exit 1
  if [ "$name" = interrupted ]; then
    PLAYWRIGHT_JSON_OUTPUT_FILE="$here/playwright/$name.json" "$playwright" test --reporter=list,json >"$work/list.out" 2>&1 &
    interrupt_when "$work/list.out" 'finishes first' "$!"
  else
    PLAYWRIGHT_JSON_OUTPUT_FILE="$here/playwright/$name.json" "$playwright" test --reporter=list,json >/dev/null 2>&1
  fi
done
head -c 1500 "$here/playwright/mixed.json" >"$here/playwright/truncated.json"
echo "regenerated fixtures in $here"
