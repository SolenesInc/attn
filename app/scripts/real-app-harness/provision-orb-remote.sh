#!/usr/bin/env bash
# Provision (or re-provision) the local OrbStack VM that remote-endpoint
# harness scenarios target as attn-remote@orb. Idempotent; safe to re-run.
set -euo pipefail

VM_NAME="${ATTN_ORB_REMOTE_NAME:-attn-remote}"
SSH_TARGET="${VM_NAME}@orb"
HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REMOTE_FIXTURE_ROOT=".attn/real-app-harness/agent-fixtures"

echo "==> Checking for orbctl"
if ! command -v orbctl >/dev/null 2>&1; then
  echo "OrbStack is required: https://orbstack.dev" >&2
  exit 1
fi

echo "==> Checking for existing VM '${VM_NAME}'"
vm_list_output="$(orbctl list -f json 2>/dev/null || orbctl list 2>/dev/null || true)"
if ! grep -q "${VM_NAME}" <<<"${vm_list_output}"; then
  echo "==> Creating VM '${VM_NAME}' (ubuntu)"
  orbctl create ubuntu "${VM_NAME}"
else
  echo "==> VM '${VM_NAME}' already exists"
fi

echo "==> Waiting for SSH on ${SSH_TARGET}"
ssh_ready=0
for _ in $(seq 1 30); do
  if ssh -o BatchMode=yes -o ConnectTimeout=5 "${SSH_TARGET}" true 2>/dev/null; then
    ssh_ready=1
    break
  fi
  sleep 2
done
if [[ "${ssh_ready}" -ne 1 ]]; then
  echo "SSH to ${SSH_TARGET} never became ready; check 'orbctl logs ${VM_NAME}'" >&2
  exit 1
fi
echo "==> SSH is ready on ${SSH_TARGET}"

echo "==> Installing base packages (git, nodejs, npm)"
ssh -o BatchMode=yes "${SSH_TARGET}" 'sudo apt-get update -qq && sudo apt-get install -y -qq git nodejs npm'

# `attn app apply` bundles with bun and typechecks with the TypeScript it
# installs itself, so a remote that cannot run bun cannot install an app at all
# — apps are a daemon surface, and the daemon is supported on Linux.
echo "==> Installing bun (attn app apply bundles with it)"
ssh -o BatchMode=yes "${SSH_TARGET}" '
  command -v bun >/dev/null 2>&1 && exit 0
  [ -x "$HOME/.bun/bin/bun" ] && exit 0
  curl -fsSL https://bun.sh/install | bash
'

echo "==> Installing the no-model agent fixture"
ssh -o BatchMode=yes "${SSH_TARGET}" "mkdir -p '${REMOTE_FIXTURE_ROOT}/bin'"
scp -q -o BatchMode=yes \
  "${HARNESS_DIR}/mockAgent.mjs" \
  "${HARNESS_DIR}/remote-agent-tripwire-shim.sh" \
  "${SSH_TARGET}:${REMOTE_FIXTURE_ROOT}/"
ssh -o BatchMode=yes "${SSH_TARGET}" "
  install -m 0755 '${REMOTE_FIXTURE_ROOT}/mockAgent.mjs' '${REMOTE_FIXTURE_ROOT}/bin/attn-harness-mock-agent'
  for name in claude codex copilot pi; do
    install -m 0755 '${REMOTE_FIXTURE_ROOT}/remote-agent-tripwire-shim.sh' \"${REMOTE_FIXTURE_ROOT}/bin/\${name}\"
  done
"

echo "==> Verifying required tools and fixtures in the VM"
missing_tools="$(ssh -o BatchMode=yes "${SSH_TARGET}" '
  missing=""
  # bun installs into ~/.bun/bin, which a non-interactive shell does not have
  # on PATH until .bashrc runs — check it where it lands.
  PATH="$HOME/.bun/bin:$PATH"
  for tool in git node python3 ss bun; do
    command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
  done
  for fixture in attn-harness-mock-agent claude codex copilot pi; do
    test -x "$HOME/.attn/real-app-harness/agent-fixtures/bin/$fixture" || missing="$missing fixture:$fixture"
  done
  echo "$missing"
')"
if [[ -n "${missing_tools// /}" ]]; then
  echo "Missing required tools in VM:${missing_tools}" >&2
  exit 1
fi
echo "==> All required tools and no-model fixtures are present"

echo "==> Provisioning summary"
echo "    VM name:     ${VM_NAME}"
echo "    SSH target:  ${SSH_TARGET}"
echo "    agent fixture: ~/${REMOTE_FIXTURE_ROOT}/bin"
echo "    model auth:    not required"
