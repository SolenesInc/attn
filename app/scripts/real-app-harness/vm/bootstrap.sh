#!/usr/bin/env bash
set -euo pipefail
[[ "$(uname -s)" == Linux ]] || { echo 'The runner requires a Linux guest' >&2; exit 1; }
missing=0
for tool in git tar flock; do
  command -v "$tool" >/dev/null || missing=1
done
if [[ "$missing" == 1 ]]; then
  sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y git tar util-linux
fi
