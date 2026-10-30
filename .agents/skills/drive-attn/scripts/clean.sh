#!/usr/bin/env bash
set -euo pipefail

name=${1:?usage: clean.sh <name>}
env_file=/tmp/drive-attn-$name.env

. "$env_file"
./attn instance clean "$name"
pnpm --dir app run real-app:mock-github stop
rm -rf "$ATTN_TOOL_HOME"
rm "$env_file"
echo "drive-attn: cleaned $name and removed $env_file"
