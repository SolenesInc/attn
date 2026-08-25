#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "The evidence recorder can only be installed on macOS" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_bundle="${repo_root}/app/src-tauri/target/evidence-recorder/AttnRecorder.app"
user_home="$(dscl . -read "/Users/$(id -un)" NFSHomeDirectory | awk '{print $2}')"
if [[ -z "${user_home}" || "${user_home}" != /* || "${user_home}" == "/" ]]; then
  echo "Could not resolve a safe user home directory" >&2
  exit 1
fi
installed_bundle="${user_home}/Applications/AttnRecorder.app"
installed_binary="${installed_bundle}/Contents/MacOS/attn-recorder"
state_dir="${user_home}/Library/Application Support/com.attn.recorder"
manifest="${state_dir}/evidence-recording.json"

stop_running_recorder() {
  [[ -f "${manifest}" ]] || return 0
  local pid command
  pid="$(/usr/bin/plutil -extract pid raw -o - "${manifest}" 2>/dev/null || true)"
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 0
  command="$(ps -p "${pid}" -o command= 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)"
  if [[ "${command}" == "${installed_binary}" ]]; then
    kill -TERM "${pid}"
    for _ in {1..50}; do
      kill -0 "${pid}" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "${pid}" 2>/dev/null; then
      echo "Recorder process ${pid} did not stop" >&2
      exit 1
    fi
  fi
}

case "${1:-install}" in
  install)
    [[ -d "${source_bundle}" ]] || {
      echo "No built recorder at ${source_bundle}; run make build-evidence-recorder first" >&2
      exit 1
    }
    stop_running_recorder
    rm -rf "${installed_bundle}"
    mkdir -p "${user_home}/Applications"
    cp -R "${source_bundle}" "${installed_bundle}"
    open "${installed_bundle}"
    for _ in {1..50}; do
      [[ -f "${manifest}" ]] && break
      sleep 0.1
    done
    [[ -f "${manifest}" ]] || {
      echo "attn-recorder.app launched without publishing ${manifest}" >&2
      exit 1
    }
    echo "Installed and opened ${installed_bundle}"
    ;;
  uninstall)
    stop_running_recorder
    rm -rf "${installed_bundle}" "${state_dir}"
    /usr/bin/tccutil reset ScreenCapture com.attn.recorder.capture >/dev/null 2>&1 || true
    echo "Removed ${installed_bundle}, ${state_dir}, and the Screen Recording grant"
    ;;
  *)
    echo "Usage: $0 [install|uninstall]" >&2
    exit 2
    ;;
esac
