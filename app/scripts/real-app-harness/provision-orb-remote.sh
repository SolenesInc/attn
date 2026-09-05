#!/usr/bin/env bash
set -euo pipefail
harness_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec node "$harness_dir/linux-runner.mjs" fixtures --provider orb --name "${ATTN_ORB_REMOTE_NAME:-attn-remote}" "$@"
