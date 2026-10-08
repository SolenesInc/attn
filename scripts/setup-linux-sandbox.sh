#!/usr/bin/env bash
set -euo pipefail

run_setup_command() {
  local command started status command_timeout=600s
  local -a monitor=(timeout --verbose --signal=KILL "$command_timeout")
  printf -v command '%q ' "$@"
  printf '[%s] Linux sandbox: starting %s(command_timeout=%s)\n' "$(date -u +%FT%TZ)" "$command" "$command_timeout"
  if [[ "$1" == sudo ]]; then
    monitor=(sudo -n "${monitor[@]}")
    shift
  fi
  started=$SECONDS
  status=0
  "${monitor[@]}" "$@" || status=$?
  if [[ "$status" != 0 ]]; then
    printf '[%s] Linux sandbox: failed %s(command_timeout=%s, elapsed=%ss, exit=%s)\n' "$(date -u +%FT%TZ)" "$command" "$command_timeout" "$((SECONDS - started))" "$status" >&2
  else
    printf '[%s] Linux sandbox: finished %s(elapsed=%ss)\n' "$(date -u +%FT%TZ)" "$command" "$((SECONDS - started))"
  fi
  return "$status"
}

run_setup_command sudo apt-get update
run_setup_command sudo apt-get install -y bubblewrap ripgrep
if [[ -e /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]]; then
  run_setup_command sudo tee /etc/apparmor.d/bwrap <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>
profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE
  run_setup_command sudo apparmor_parser -r /etc/apparmor.d/bwrap
fi
run_setup_command bwrap --unshare-all --ro-bind / / --proc /proc --dev /dev -- /bin/true
