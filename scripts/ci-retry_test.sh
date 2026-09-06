#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
retry="$root/scripts/ci-retry.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-ci-retry-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

# Fails until called `until` times; the count is a file so it survives execs.
make_flaky() {
  local name="$1" until="$2" code="$3"
  cat >"$work/$name" <<EOF
#!/usr/bin/env bash
count=\$(( \$(cat "$work/$name.count" 2>/dev/null || echo 0) + 1 ))
echo "\$count" >"$work/$name.count"
if [ "\$count" -lt $until ]; then
  echo "HTTP Error 504: Gateway Time-out" >&2
  exit $code
fi
echo "installed"
EOF
  chmod +x "$work/$name"
  rm -f "$work/$name.count"
}

calls() { cat "$work/$1.count" 2>/dev/null || echo 0; }

fail() { echo "ci-retry test: $1" >&2; exit 1; }

make_flaky first-try 1 100
out="$("$retry" --delay 0 -- "$work/first-try")"
[ "$out" = "installed" ] || fail "expected the command's stdout, got '$out'"
[ "$(calls first-try)" = "1" ] || fail "a passing command was run $(calls first-try) times, want 1"

make_flaky one-504 2 100
"$retry" --delay 0 -- "$work/one-504" >"$work/out.txt" 2>"$work/err.txt" \
  || fail "one transient failure should not fail the step"
[ "$(calls one-504)" = "2" ] || fail "expected 2 attempts, got $(calls one-504)"
grep -q "attempt 1 of 3" "$work/err.txt" || fail "the retry never named the attempt: $(cat "$work/err.txt")"

make_flaky always-down 99 42
status=0
"$retry" --attempts 3 --delay 0 -- "$work/always-down" >"$work/out.txt" 2>"$work/err.txt" || status=$?
[ "$status" = "42" ] || fail "expected the command's exit status 42, got $status"
[ "$(calls always-down)" = "3" ] || fail "expected exactly 3 attempts, got $(calls always-down)"
grep -q "failed on all 3 attempts (last exit 42)" "$work/err.txt" \
  || fail "giving up did not name the limit: $(cat "$work/err.txt")"

make_flaky once 99 7
status=0
"$retry" --attempts 1 --delay 0 -- "$work/once" >/dev/null 2>&1 || status=$?
[ "$status" = "7" ] || fail "expected exit 7 with a single attempt, got $status"
[ "$(calls once)" = "1" ] || fail "expected 1 attempt, got $(calls once)"

printf '#!/usr/bin/env bash\nprintf "%%s|" "$@"\n' >"$work/echo-args"
chmod +x "$work/echo-args"
out="$("$retry" --delay 0 -- "$work/echo-args" -y 'ppa:fish-shell/release-4' 'two words')"
[ "$out" = "-y|ppa:fish-shell/release-4|two words|" ] || fail "arguments were mangled: '$out'"

status=0; "$retry" --delay 0 -- >/dev/null 2>&1 || status=$?
[ "$status" = "2" ] || fail "an empty command should exit 2, got $status"
status=0; "$retry" --attempts 0 -- /bin/true >/dev/null 2>&1 || status=$?
[ "$status" = "2" ] || fail "--attempts 0 should exit 2, got $status"
status=0; "$retry" --attempts abc -- /bin/true >/dev/null 2>&1 || status=$?
[ "$status" = "2" ] || fail "a non-numeric --attempts should exit 2, got $status"

echo "ci-retry: all tests passed"
