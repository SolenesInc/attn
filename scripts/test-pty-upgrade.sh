#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
if ! command -v lsof >/dev/null; then
  echo 'PTY upgrade test requires lsof to discover historical daemon listeners (install lsof).' >&2
  exit 1
fi
if ! command -v fish >/dev/null; then
  echo 'PTY upgrade test requires fish to start agents through a fish login shell (install fish).' >&2
  exit 1
fi
test_root="$(mktemp -d "${TMPDIR:-/tmp}/attn-pty-upgrade.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

if [ -z "${ATTN_TEST_PTY_HOST:-}" ]; then
  ATTN_TEST_PTY_HOST="$(bash scripts/lib/rust-build-store.sh provide attn-pty-host)"
  export ATTN_TEST_PTY_HOST
fi
if [ -z "${ATTN_TEST_PTY_HOST_VARIANTS:-}" ]; then
  ATTN_TEST_PTY_HOST_VARIANTS="$(bash scripts/lib/rust-build-store.sh provide-variants pty-host/test-variants)"
  export ATTN_TEST_PTY_HOST_VARIANTS
fi
download_release() {
  local url="$1" dir="$2" arch
  mkdir -p "$dir"
  case "$(go env GOOS)" in
    linux)
      arch="$(go env GOARCH)"
      curl -fsSL --retry 3 -o "$dir/attn" "$url/attn-linux-$arch"
      curl -fsSL --retry 3 -o "$dir/attn-pty-host" "$url/attn-pty-host-linux-$arch"
      chmod +x "$dir/attn" "$dir/attn-pty-host"
      if curl -fsSL --retry 3 -o "$dir/attnd" "$url/attnd-linux-$arch"; then
        chmod +x "$dir/attnd"
      fi
      ;;
    darwin)
      curl -fsSL --retry 3 "$url/attn_aarch64.app.tar.gz" |
        tar -xz --strip-components=3 -C "$dir" attn.app/Contents/MacOS
      ;;
    *)
      echo "No released attn for $(go env GOOS)." >&2
      exit 1
      ;;
  esac
}
if [ -z "${ATTN_UPGRADE_RELEASE_BIN:-}" ]; then
  download_release https://github.com/victorarias/attn/releases/latest/download "$test_root/release"
  export ATTN_UPGRADE_RELEASE_BIN="$test_root/release/attn"
  export ATTN_UPGRADE_RELEASE_HOST="$test_root/release/attn-pty-host"
fi
if [ -z "${ATTN_UPGRADE_ADDRESS_RELEASE_BIN:-}" ]; then
  last_release_accepting_outpost_addresses=v0.13.0
  download_release "https://github.com/victorarias/attn/releases/download/$last_release_accepting_outpost_addresses" "$test_root/address-release"
  export ATTN_UPGRADE_ADDRESS_RELEASE_BIN="$test_root/address-release/attn"
  export ATTN_UPGRADE_ADDRESS_RELEASE_HOST="$test_root/address-release/attn-pty-host"
fi
if [ -z "${ATTN_UPGRADE_WORKER_RELEASE_BIN:-}" ]; then
  last_release_with_dedicated_workers=v0.15.0
  download_release "https://github.com/victorarias/attn/releases/download/$last_release_with_dedicated_workers" "$test_root/worker-release"
  export ATTN_UPGRADE_WORKER_RELEASE_BIN="$test_root/worker-release/attn"
  export ATTN_UPGRADE_WORKER_RELEASE_HOST="$test_root/worker-release/attn-pty-host"
fi
echo "Previous release under test: $("$ATTN_UPGRADE_RELEASE_BIN" --version)"
if [ -z "${ATTN_E2E_BIN:-}" ]; then
  mkdir -p "$test_root/current"
  go build -ldflags "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$(bash scripts/snapshot-format.sh)" \
    -o "$test_root/current/attnd" ./cmd/attnd
  bash scripts/rust-cli.sh build --out "$test_root/current/attn"
  export ATTN_E2E_BIN="$test_root/current/attn"
fi
go test ./cmd/attnd -run '^(TestSessionsSurviveDaemonRestartsAndSharedHostGenerations|TestAnUpdateFromTheRelease.*|TestAFreshInstallFindsItsHost.*|TestATerminalOpenedBeforeTheFirstHostCheck.*|TestAfterAnUpdateAReboot.*|TestAHostThatCannotRun.*|TestAMachineWithOnlyAttn.*|TestDirectLaunchStartsAgents.*|TestAPhoneReattachesToAHost.*|TestKillingTheDaemonDuringAHostMove.*|TestAnOutpostOnTheRelease.*|TestAnOutpostAddedByAddress.*)$' -count=1 -timeout=180s -v "$@"
