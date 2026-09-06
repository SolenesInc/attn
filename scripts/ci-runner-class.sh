#!/usr/bin/env bash
set -euo pipefail

# `ubuntu-24.04` is Blacksmith or GitHub-hosted per job; they differ ~2.4x on
# compile-bound steps, so a timing receipt is unreadable without the class.

host=github-hosted
if [[ -f /etc/profile.d/blacksmith.sh || -n "${BLACKSMITH_ENV:-}" ]]; then
  host=blacksmith
fi

cpus="$(nproc)"
memory_gb="$(awk '/^MemTotal:/ { printf "%.0f", $2 / 1024 / 1024 }' /proc/meminfo)"
model="$(awk -F': ' '/^model name/ { print $2; exit }' /proc/cpuinfo)"
class="${host} ${cpus}vcpu/${memory_gb}GB"

echo "runner class: ${class} (${model:-unknown cpu}, label ${RUNNER_NAME:-unknown})"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  echo "\`${GITHUB_JOB:-job}\` ran on **${class}** — ${model:-unknown cpu}" >>"${GITHUB_STEP_SUMMARY}"
fi
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  echo "class=${class}" >>"${GITHUB_OUTPUT}"
fi
