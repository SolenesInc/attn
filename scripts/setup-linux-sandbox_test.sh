#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/attn-linux-sandbox-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"

cat >"$work/bin/sudo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" != -n ]] || shift
exec "$@"
EOF

cat >"$work/bin/timeout" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
shift 3
if [[ "$*" == "${EXPIRED_COMMAND:-}" ]]; then
  exit 137
fi
exec "$@"
EOF

cat >"$work/bin/apt-get" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == update ]]; then
  echo 'apt update: waiting for mirror'
  exit "${APT_UPDATE_STATUS:-0}"
fi
echo 'apt install: packages ready'
EOF

cat >"$work/bin/bwrap" <<'EOF'
#!/usr/bin/env bash
echo 'sandbox probe succeeded'
EOF

cat >"$work/bin/tee" <<'EOF'
#!/usr/bin/env bash
cat
EOF

cat >"$work/bin/apparmor_parser" <<'EOF'
#!/usr/bin/env bash
echo 'AppArmor profile loaded'
EOF
chmod +x "$work/bin/"*
export PATH="$work/bin:$PATH"

bash "$root/scripts/setup-linux-sandbox.sh" >"$work/success.out" 2>&1
grep -Fq 'starting sudo apt-get update (command_timeout=600s)' "$work/success.out"
grep -Fq 'apt update: waiting for mirror' "$work/success.out"
grep -Fq 'finished sudo apt-get update (elapsed=' "$work/success.out"
grep -Fq 'sandbox probe succeeded' "$work/success.out"

expect_failure() {
  local expected_status="$1" expected_message="$2" status=0
  bash "$root/scripts/setup-linux-sandbox.sh" >"$work/failure.out" 2>&1 || status=$?
  if [[ "$status" != "$expected_status" ]]; then
    cat "$work/failure.out" >&2
    echo "expected exit $expected_status, got $status" >&2
    exit 1
  fi
  grep -Fq "$expected_message" "$work/failure.out"
  if grep -Fq 'sandbox probe succeeded' "$work/failure.out"; then
    echo 'sandbox setup continued after a failed command' >&2
    exit 1
  fi
}

export EXPIRED_COMMAND='apt-get update'
expect_failure 137 'failed sudo apt-get update (command_timeout=600s, '
if grep -Fq 'apt install: packages ready' "$work/failure.out"; then
  echo 'sandbox setup installed packages after update timed out' >&2
  exit 1
fi
export EXPIRED_COMMAND='apt-get install -y bubblewrap ripgrep'
expect_failure 137 'failed sudo apt-get install -y bubblewrap ripgrep (command_timeout=600s, '
export EXPIRED_COMMAND='bwrap --unshare-all --ro-bind / / --proc /proc --dev /dev -- /bin/true'
expect_failure 137 'failed bwrap '
unset EXPIRED_COMMAND
export APT_UPDATE_STATUS=42
expect_failure 42 'failed sudo apt-get update '
grep -Fq 'exit=42' "$work/failure.out"
export APT_UPDATE_STATUS=137
expect_failure 137 'failed sudo apt-get update '
if grep -Fq 'exceeded' "$work/failure.out"; then
  echo 'sandbox setup mistook a killed command for timeout expiry' >&2
  exit 1
fi

echo 'Linux sandbox setup tests passed'
