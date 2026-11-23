#!/usr/bin/env bash
set -euo pipefail

cat >&2 <<'EOF'
scripts/land.sh is retired. attn now lands on main with the `land` CLI.

Run `land guide` and read `.agents/skills/land-on-main/SKILL.md`, then land with:
  land

To run the baseline landing tests alone (replacing `scripts/land.sh --baseline`):
  ATTN_LAND_BASELINE=1 land

If git's pre-push hook sent you here after land's gate passed, core.hooksPath
points at a checkout whose .githooks/pre-push predates land. Bring that checkout
up to date with main, then run land again.

If `land` is not on your PATH, install it with:
  mise use -g github:SolenesInc/land
or:
  mkdir -p ~/.local/bin && curl -fsSL "https://github.com/SolenesInc/land/releases/latest/download/land-$(uname -s)-$(uname -m)" -o ~/.local/bin/land && chmod +x ~/.local/bin/land
EOF
exit 1
