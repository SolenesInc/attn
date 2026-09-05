#!/usr/bin/env bash
set -euo pipefail
sudo apt-get update -qq
sudo apt-get install -y bubblewrap ripgrep
# Ubuntu restricts user namespaces; grant them only to the sandbox executable.
if [[ -e /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]]; then
  sudo tee /etc/apparmor.d/bwrap >/dev/null <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>
profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE
  sudo apparmor_parser -r /etc/apparmor.d/bwrap
fi
bwrap --unshare-all --ro-bind / / --proc /proc --dev /dev -- /bin/true
