#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
check="$root/scripts/check-home-gateway-compat"
base="$root/internal/protocol/homegateway/schema.json"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-home-gateway-compat-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

fail() { echo "check-home-gateway-compat test: $1" >&2; exit 1; }

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

edit removed-field 'delete s.HomeGatewaySession.properties.title; s.HomeGatewaySession.required = s.HomeGatewaySession.required.filter(f => f !== "title")'
refuses removed-field "HomeGatewaySession.title: removed or renamed"

edit renamed-field 's.HomeGatewaySession.properties.name = s.HomeGatewaySession.properties.title; delete s.HomeGatewaySession.properties.title'
refuses renamed-field "HomeGatewaySession.title: removed or renamed"

edit changed-type 's.HomeGatewaySession.properties.offline = {type: "string"}'
refuses changed-type "HomeGatewaySession.offline: type changed"

edit became-required 's.HomeGatewaySession.required.push("host")'
refuses became-required "HomeGatewaySession.host: became required"

edit became-optional 's.HomeGatewaySession.required = s.HomeGatewaySession.required.filter(f => f !== "offline")'
refuses became-optional "HomeGatewaySession.offline: became optional"

edit added-required 's.HomeGatewaySession.properties.pinned = {type: "boolean"}; s.HomeGatewaySession.required.push("pinned")'
refuses added-required "HomeGatewaySession.pinned: added as required"

edit removed-member 's.HomeGatewaySessionState.enum = s.HomeGatewaySessionState.enum.filter(m => m !== "scheduled")'
refuses removed-member "HomeGatewaySessionState: member scheduled removed"

edit removed-message 'delete s.HomeGatewayRefused'
refuses removed-message "HomeGatewayRefused: removed"

edit added-optional 's.HomeGatewaySession.properties.pinned = {type: "boolean"}; s.HomeGatewaySessionState.enum.push("paused"); s.HomeGatewayHeadsUp = {type: "object", properties: {}}'
accepts added-optional "HomeGatewaySession.pinned, HomeGatewaySessionState: member paused, HomeGatewayHeadsUp"

echo "check-home-gateway-compat: all cases passed"
