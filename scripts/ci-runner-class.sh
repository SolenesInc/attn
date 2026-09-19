#!/usr/bin/env bash
set -euo pipefail

# GitHub labels can resolve to different hardware per job. Timing receipts need
# the measured CPU and memory beside the label.

host=github-hosted
if [[ -f /etc/profile.d/blacksmith.sh || -n "${BLACKSMITH_ENV:-}" ]]; then
  host=blacksmith
fi

if [[ "$(uname -s)" == Darwin ]]; then
  cpus="$(sysctl -n hw.logicalcpu)"
  memory_gb="$(sysctl -n hw.memsize | awk '{ printf "%.0f", $1 / 1024 / 1024 / 1024 }')"
  model="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || sysctl -n hw.model)"
else
  cpus="$(nproc)"
  memory_gb="$(awk '/^MemTotal:/ { printf "%.0f", $2 / 1024 / 1024 }' /proc/meminfo)"
  model="$(awk -F': ' '/^model name/ { print $2; exit }' /proc/cpuinfo)"
fi
class="${host} ${cpus}vcpu/${memory_gb}GB"

echo "runner class: ${class} (${model:-unknown cpu}, label ${RUNNER_NAME:-unknown})"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "\`${GITHUB_JOB:-job}\` ran on **${class}** — ${model:-unknown cpu}" >>"${GITHUB_STEP_SUMMARY}"
fi
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  echo "class=${class}" >>"${GITHUB_OUTPUT}"
fi
