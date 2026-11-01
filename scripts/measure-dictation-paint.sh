#!/usr/bin/env bash
# Usage: . /tmp/drive-attn-<name>.env && scripts/measure-dictation-paint.sh <field-selector> <clip.f32>... [--mic <clip.aiff>...] [--rounds N]
# Drives dictation in the packaged app of the sourced instance and prints one JSON line per dictation.
# A .f32 clip streams through dictation_clip and stops at its last sample; an .aiff after --mic plays through the speakers into the real microphone and stops when playback ends.
set -euo pipefail

[ -n "${ATTN_INSTANCE:-}" ] && [ "$ATTN_INSTANCE" != "dev" ] || { echo "source a named instance's drive-attn env first" >&2; exit 2; }
selector=$1; shift
rounds=1
mode=clip
clips=()
while [ $# -gt 0 ]; do
  case $1 in
    --rounds) rounds=$2; shift 2 ;;
    --mic) mode=mic; shift ;;
    *) clips+=("$mode:$1"); shift ;;
  esac
done

json() { python3 -I -c 'import json, sys; print(json.dumps(dict(zip(sys.argv[1::2], sys.argv[2::2]))))' "$@"; }
call() { pnpm --silent --dir app run real-app:bridge-cli -- "$@"; }
receipts() { call dictation_get_state | python3 -I -c 'import json,sys; print(len(json.load(sys.stdin)["receipts"]))'; }
seconds() { ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }

for _ in $(seq "$rounds"); do
  for entry in "${clips[@]}"; do
    kind=${entry%%:*}; clip=${entry#*:}
    if [ "$kind" = clip ]; then
      call dictation_clip "{\"path\":\"$clip\",\"stopAtEnd\":true}" >/dev/null
      length=$(python3 -I -c "import os,sys; print(os.path.getsize(sys.argv[1]) / 4 / 16000)" "$clip")
    else
      call dictation_clip '{"path":null}' >/dev/null
      length=$(seconds "$clip")
    fi
    call dom_type "$(json selector "$selector" text "")" >/dev/null
    call dom_focus "$(json selector "$selector")" >/dev/null
    before=$(receipts)
    call hotkey_press '{"window":"main"}' >/dev/null
    call dom_wait '{"selector":"[data-testid=\"dictation-pill\"][data-phase=\"recording\"]","timeoutMs":15000}' >/dev/null
    if [ "$kind" = mic ]; then afplay "$clip"; call hotkey_press '{"window":"main"}' >/dev/null; fi
    for _ in $(seq 60); do [ "$(receipts)" -gt "$before" ] && break; sleep 0.5; done
    raw=$(call dictation_get_state)
    call dom_value "$(json selector "$selector")" | python3 -I -c '
import json, os, sys
receipt = json.loads(sys.argv[4])["receipts"][-1]
receipt.update(kind=sys.argv[1], clip=os.path.basename(sys.argv[2]), clipSeconds=float(sys.argv[3]), load=os.getloadavg()[0], fieldAfterRaw=json.load(sys.stdin)["value"])
print(json.dumps(receipt))' "$kind" "$clip" "$length" "$raw"
    for _ in $(seq 120); do
      call dictation_get_state | python3 -I -c 'import json, sys; sys.exit(any(d["phase"] in ("starting", "recording", "transcribing", "refining") for d in json.load(sys.stdin)["dictations"]))' && break
      sleep 0.5
    done
  done
done
