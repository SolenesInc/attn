#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
check="$root/scripts/check-gateway-compat"
base="$root/internal/protocol/gateway/schema.json"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-gateway-compat-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

fail() { echo "check-gateway-compat test: $1" >&2; exit 1; }

# edit <name> <js>: writes $work/<name>.json, the committed schema after the statement runs against `s`.
edit() {
  node -e 'const fs = require("fs"); const s = JSON.parse(fs.readFileSync(process.argv[1], "utf8")); eval(process.argv[2]); fs.writeFileSync(process.argv[3], JSON.stringify(s));' \
    "$base" "$2" "$work/$1.json"
}

refuses() {
  local name="$1" want="$2" status=0
  "$check" "$base" "$work/$name.json" >"$work/out.txt" 2>"$work/err.txt" || status=$?
  [ "$status" = 1 ] || fail "$name: exit $status, want 1; stderr: $(cat "$work/err.txt")"
  grep -qF -- "$want" "$work/err.txt" || fail "$name: stderr does not name '$want': $(cat "$work/err.txt")"
}

accepts() {
  local name="$1" want="$2"
  "$check" "$base" "$work/$name.json" >"$work/out.txt" 2>"$work/err.txt" || fail "$name: refused: $(cat "$work/err.txt")"
  grep -qF -- "$want" "$work/out.txt" || fail "$name: stdout does not name '$want': $(cat "$work/out.txt")"
}

edit unchanged ''
accepts unchanged "matches"

edit removed-field 'delete s.GatewaySession.properties.title; s.GatewaySession.required = s.GatewaySession.required.filter(f => f !== "title")'
refuses removed-field "GatewaySession.title: removed or renamed"

edit renamed-field 's.GatewaySession.properties.name = s.GatewaySession.properties.title; delete s.GatewaySession.properties.title'
refuses renamed-field "GatewaySession.title: removed or renamed"

edit changed-type 's.GatewaySession.properties.offline = {type: "string"}'
refuses changed-type "GatewaySession.offline: type changed"

edit became-required 's.GatewaySession.required.push("host")'
refuses became-required "GatewaySession.host: became required"

edit became-optional 's.GatewaySession.required = s.GatewaySession.required.filter(f => f !== "offline")'
refuses became-optional "GatewaySession.offline: became optional"

edit added-required 's.GatewaySession.properties.pinned = {type: "boolean"}; s.GatewaySession.required.push("pinned")'
refuses added-required "GatewaySession.pinned: added as required"

edit removed-member 's.GatewaySessionState.enum = s.GatewaySessionState.enum.filter(m => m !== "scheduled")'
refuses removed-member "GatewaySessionState: member scheduled removed"

edit removed-message 'delete s.GatewayRefused'
refuses removed-message "GatewayRefused: removed"

edit added-optional 's.GatewaySession.properties.pinned = {type: "boolean"}; s.GatewaySessionState.enum.push("paused"); s.GatewayHeadsUp = {type: "object", properties: {}}'
accepts added-optional "GatewaySession.pinned, GatewaySessionState: member paused, GatewayHeadsUp"

echo "check-gateway-compat: all cases passed"
