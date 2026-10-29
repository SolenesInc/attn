#!/usr/bin/env bash
set -euo pipefail

name=${1:?usage: write-env.sh <name>}
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
env_file=/tmp/drive-attn-$name.env
cd "$root"

selection=$(./attn instance-env "$name")
eval "$selection"
mock_github_port=$(./attn instance resolve --field mockGitHubPort)
tool_home=$(mktemp -d /tmp/drive-attn-toolhome.XXXX)
mock_agent=$root/app/scripts/real-app-harness/mockAgent.mjs

cat > "$env_file" <<EOF
$selection
cd '$root'
unset ATTN_INSIDE_APP ATTN_TERMINAL_ID ATTN_SESSION_ID ATTN_AGENT ATTN_WRAPPER_PATH ATTN_CODEX_SHARED_PROFILE CODEX_THREAD_ID CODEX_HOME
export ATTN_CLAUDE_EXECUTABLE='$mock_agent' ATTN_CODEX_EXECUTABLE='$mock_agent' ATTN_HEADLESS_TASKS=off
export ATTN_TOOL_HOME=$tool_home ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:1
export ATTN_MOCK_GH_URL=http://127.0.0.1:$mock_github_port ATTN_MOCK_GH_TOKEN=test-token ATTN_MOCK_GH_HOST=mock.github.local
DRIVE_ENV=$env_file
wsctl() { go run ./scripts/wsctl "\$@"; }
bridge() { pnpm --silent --dir app run real-app:bridge-cli -- "\$@"; }
[ "\$ATTN_INSTANCE" = $name ] || { echo "drive-attn: $env_file does not select instance $name" >&2; return 1; }
EOF
echo "$env_file"
