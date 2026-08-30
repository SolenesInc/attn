#!/bin/sh
set -eu

name=${0##*/}
scenario=${ATTN_AGENT_TRIPWIRE_SCENARIO:-unattributed}
ledger=${ATTN_AGENT_TRIPWIRE_LEDGER:-}

if [ -z "$ledger" ]; then
  printf 'attn remote harness agent tripwire: %s has no ATTN_AGENT_TRIPWIRE_LEDGER\n' "$name" >&2
  exit 97
fi

mkdir -p "$(dirname "$ledger")"
full=$(printf '%s ' "$name" "$@" | tr '\n\t' '  ' | sed 's/ *$//')
# A real agent launch measured 8584 argv bytes; 512 names it without archiving the prompt.
argv=$(printf '%.512s' "$full")
if [ "${#full}" -gt 512 ]; then
  argv="$argv... (+$((${#full} - 512)) chars)"
fi
printf '%s\t%s\n' "$scenario" "$argv" >> "$ledger"
printf 'attn remote harness agent tripwire: scenario %s must not run the real %s; this run fails on it.\nledger: %s\n' "$scenario" "$name" "$ledger" >&2
exit 97
