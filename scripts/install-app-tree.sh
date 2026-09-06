#!/usr/bin/env bash
set -euo pipefail

# Copies an already-staged app tree into PROFILE's install location and ensures
# its daemon. `make install` stages the tree first; `make install-staged` takes
# one another job built, so a CI shard installs without the toolchain.

profile="${PROFILE:-}"
attn="${ATTN_BIN:?ATTN_BIN must name the attn binary that resolves profile paths}"
daemon_unset="${PROFILE_DAEMON_UNSET:?PROFILE_DAEMON_UNSET must carry the env -u flags}"
routing_vars="${PROFILE_ROUTING_VARS:?PROFILE_ROUTING_VARS must list the routing variables}"
worktree="${WORKTREE:-$PWD}"

app_name="$("${attn}" profile resolve --profile "${profile}" --field appName)"
ws_port="$("${attn}" profile resolve --profile "${profile}" --field wsPort)"
label="$("${attn}" profile resolve --profile "${profile}" --field label)"
app_bundle="$("${attn}" profile resolve --profile "${profile}" --field appPath)"
app_binary="$("${attn}" profile resolve --profile "${profile}" --field appDaemon)"

if [[ "$(uname -s)" == "Darwin" ]]; then
  staged="app/src-tauri/target/release/bundle/macos/${app_name}.app"
else
  staged="app/src-tauri/target/release/linux-tree/${app_name}"
fi

if [[ ! -d "${staged}" ]]; then
  echo "No staged app tree at ${staged}; run \`make install${profile:+ PROFILE=${profile}}\` to build one, or download the one a build job uploaded" >&2
  exit 1
fi

echo ">>> Installing ${label}: ${app_bundle} (port=${ws_port})"
mkdir -p "$(dirname "${app_bundle}")"
# Quit a running instance first. macOS keeps the running image via mmap,
# so rm -rf + cp alone would leave an old process out of a deleted bundle.
"${attn}" profile stop-app --profile "${profile}" >/dev/null
rm -rf "${app_bundle}"
cp -r "${staged}" "${app_bundle}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  "${attn}" profile register-scheme --profile "${profile}"
fi

if [[ -n "${profile}" ]]; then
  leaked=""
  for var in ${routing_vars}; do
    if printenv "${var}" >/dev/null; then
      leaked="${leaked} ${var}"
    fi
  done
  if [[ -n "${leaked}" ]]; then
    echo ">>> ignoring inherited routing env for this install:${leaked}"
    echo ">>> this shell still routes elsewhere — select the profile with attn profile-env"
  fi
  # shellcheck disable=SC2086 # the unset flags are a word list by construction
  env ${daemon_unset} ATTN_PROFILE="${profile}" "${app_binary}" daemon ensure >/dev/null
  # Install time is the only moment the worktree behind a profile is known
  # for certain, so record it here for cleanup tooling to read back.
  "${attn}" profile set-origin "${profile}" --worktree "${worktree}" >/dev/null || true
else
  "${app_binary}" daemon ensure >/dev/null
fi

echo "Installed ${app_bundle} (profile=${label}, port=${ws_port})"
