#!/usr/bin/env bash
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work=/tmp/attn-test-history-fixtures
rm -rf "$work"
mkdir -p "$work"
cp -R "$here/projects/." "$work/"

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

cd "$work/cargo" || exit 1
cargo test --message-format=json --no-fail-fast -p good >"$here/cargo/mixed.log" 2>&1
cargo test --message-format=json --no-fail-fast -p broken >"$here/cargo/build-failure.log" 2>&1
FIXTURE_WAIT="$work/never" cargo test --message-format=json --no-fail-fast -p good --test wire --test slow >"$here/cargo/interrupted.log" 2>&1 &
interrupt_when "$here/cargo/interrupted.log" 'Running tests/slow.rs' "$!"
sed -i.bak "s#$work#/fixture#g" "$here"/cargo/*.log && rm "$here"/cargo/*.log.bak

echo "regenerated fixtures in $here"
