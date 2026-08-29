#!/usr/bin/env bash
set -euo pipefail

event_name="${1:?usage: main-route.sh <event-name> [base-branch] [head-branch]}"
base_branch="${2:-}"
head_branch="${3:-}"

if [[ "$event_name" != "pull_request" || "$base_branch" != "main" ]]; then
  echo "main route: ${event_name} for ${base_branch:-no base}, no main routing rule applies"
  exit 0
fi

case "$head_branch" in
  hotfix/*)
    echo "main route: ${head_branch} may target main"
    exit 0
    ;;
  epic/release-train)
    if git show-ref --verify --quiet refs/remotes/origin/next; then
      echo "main route: epic/release-train was only allowed before next existed" >&2
      exit 1
    fi
    echo "main route: epic/release-train may bootstrap main before next exists"
    exit 0
    ;;
esac

if [[ "$head_branch" =~ ^release/v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "main route: ${head_branch} may target main"
  exit 0
fi

cat >&2 <<EOF
main route: ${head_branch:-unknown branch} may not target main.

Normal work and completed epic branches target next. Only frozen release/vX.Y.Z
candidates, urgent hotfix/* branches, and the one-time epic/release-train
bootstrap may target main.
EOF
exit 1
