#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
check="$root/scripts/check-relay-privacy"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-relay-privacy-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

fail() { echo "check-relay-privacy test: $1" >&2; exit 1; }

cd "$work"
git init -q
printf 'sign("attn-cloud/v1 register")\n' >wire.go
git add wire.go
"$check" || fail "refused the wire's signed strings"

for line in 'see SolenesInc/attn-cloud' 'attn-cloud/v1 noise in attn-cloud' 'a Durable Object' 'run it under workerd' 'Cloudflare logs'; do
  printf '%s\n' "$line" >doc.md
  git add doc.md
  if "$check" 2>err.txt; then fail "accepted: $line"; fi
  grep -qF 'doc.md:1:' err.txt || fail "does not name the file for: $line: $(cat err.txt)"
done
echo "check-relay-privacy: all cases passed"
