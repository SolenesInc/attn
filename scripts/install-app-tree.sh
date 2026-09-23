#!/usr/bin/env bash
set -euo pipefail

instance="${INSTANCE:-}"
attn="${ATTN_BIN:?ATTN_BIN must name the attn binary that resolves instance paths}"
daemon_unset="${INSTANCE_DAEMON_UNSET:?INSTANCE_DAEMON_UNSET must carry the env -u flags}"
routing_vars="${INSTANCE_ROUTING_VARS:?INSTANCE_ROUTING_VARS must list the routing variables}"
worktree="${WORKTREE:-$PWD}"

app_name="$("${attn}" instance resolve --instance "${instance}" --field appName)"
ws_port="$("${attn}" instance resolve --instance "${instance}" --field wsPort)"
label="$("${attn}" instance resolve --instance "${instance}" --field label)"
app_bundle="$("${attn}" instance resolve --instance "${instance}" --field appPath)"
app_binary="$("${attn}" instance resolve --instance "${instance}" --field appDaemon)"

if [[ "$(uname -s)" == "Darwin" ]]; then
  staged="app/src-tauri/target/staged/${app_name}.app"
else
  staged="app/src-tauri/target/staged/linux-tree/${app_name}"
fi

if [[ ! -d "${staged}" ]]; then
  echo "No staged app tree at ${staged}; run \`make install${instance:+ INSTANCE=${instance}}\` to build one, or download the one a build job uploaded" >&2
  exit 1
fi

echo ">>> Installing ${label}: ${app_bundle} (port=${ws_port})"
mkdir -p "$(dirname "${app_bundle}")"
# Quit a running instance first. macOS keeps the running image via mmap,
# so rm -rf + cp alone would leave an old process out of a deleted bundle.
"${attn}" instance stop-app --instance "${instance}" >/dev/null
rm -rf "${app_bundle}"
cp -r "${staged}" "${app_bundle}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  "${attn}" instance register-scheme --instance "${instance}"
fi

if [[ -n "${instance}" ]]; then
  leaked=""
  for var in ${routing_vars}; do
    if printenv "${var}" >/dev/null; then
      leaked="${leaked} ${var}"
    fi
  done
  if [[ -n "${leaked}" ]]; then
    echo ">>> ignoring inherited routing env for this install:${leaked}"
    echo ">>> this shell still routes elsewhere — select the instance with attn instance-env"
  fi
  # shellcheck disable=SC2086 # the unset flags are a word list by construction
  env ${daemon_unset} ATTN_INSTANCE="${instance}" "${app_binary}" daemon ensure >/dev/null
  # Install time is the only moment the worktree behind an instance is known
  # for certain, so record it here for cleanup tooling to read back.
  "${attn}" instance set-origin "${instance}" --worktree "${worktree}" >/dev/null || true
else
  "${app_binary}" daemon ensure >/dev/null
fi

echo "Installed ${app_bundle} (instance=${label}, port=${ws_port})"
