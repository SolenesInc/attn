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
# Last daemon revision before the Rust host: never build both sides from HEAD.
legacy_revision=f68ab3f02f329f46d3b9b3d1e7603f545c27dd50
test_root="$(mktemp -d "${TMPDIR:-/tmp}/attn-pty-upgrade.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

if [ -z "${ATTN_TEST_PTY_HOST:-}" ]; then
  make build-pty-host
  export ATTN_TEST_PTY_HOST="$root/pty-host/target/release/attn-pty-host"
fi
if [ -z "${ATTN_UPGRADE_OLD_BIN:-}" ]; then
  mkdir -p "$test_root/legacy"
  git archive "$legacy_revision" | tar -x -C "$test_root/legacy"
  platform="$(go env GOOS)_$(go env GOARCH)"
  if [ -d "third_party/ghostty-vt/$platform" ] && cmp -s ghostty-vt.pin "$test_root/legacy/ghostty-vt.pin" && cmp -s ghostty-vt-native.lock "$test_root/legacy/ghostty-vt-native.lock"; then
    mkdir -p "$test_root/legacy/third_party/ghostty-vt"
    cp -R "third_party/ghostty-vt/$platform" "$test_root/legacy/third_party/ghostty-vt/$platform"
  else
    (cd "$test_root/legacy" && bash scripts/build-libghostty-vt.sh)
  fi
  (cd "$test_root/legacy" && go build \
    -ldflags "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$(bash scripts/snapshot-format.sh)" \
    -o "$test_root/attn-legacy" ./cmd/attn)
  export ATTN_UPGRADE_OLD_BIN="$test_root/attn-legacy"
fi
if [ -z "${ATTN_UPGRADE_RELEASE_BIN:-}" ]; then
  mkdir -p "$test_root/release"
  latest=https://github.com/victorarias/attn/releases/latest/download
  case "$(go env GOOS)" in
    linux)
      arch="$(go env GOARCH)"
      curl -fsSL --retry 3 -o "$test_root/release/attn" "$latest/attn-linux-$arch"
      curl -fsSL --retry 3 -o "$test_root/release/attn-pty-host" "$latest/attn-pty-host-linux-$arch"
      chmod +x "$test_root/release/attn" "$test_root/release/attn-pty-host"
      ;;
    darwin)
      curl -fsSL --retry 3 "$latest/attn_aarch64.app.tar.gz" |
        tar -xz --strip-components=3 -C "$test_root/release" attn.app/Contents/MacOS/attn attn.app/Contents/MacOS/attn-pty-host
      ;;
    *)
      echo "No released attn for $(go env GOOS)." >&2
      exit 1
      ;;
  esac
  export ATTN_UPGRADE_RELEASE_BIN="$test_root/release/attn"
  export ATTN_UPGRADE_RELEASE_HOST="$test_root/release/attn-pty-host"
fi
echo "Previous release under test: $("$ATTN_UPGRADE_RELEASE_BIN" --version)"
if [ -z "${ATTN_E2E_BIN:-}" ]; then
  go build -ldflags "-X github.com/victorarias/attn/internal/buildinfo.SnapshotFormat=$(bash scripts/snapshot-format.sh)" \
    -o "$test_root/attn-current" ./cmd/attn
  export ATTN_E2E_BIN="$test_root/attn-current"
fi
go test ./cmd/attn -run '^(TestSessionsSurviveDaemonUpgradesAndSharedHostGenerations|TestAnUpgradedLegacyWorkerKeepsItsModelAndEffortOnReload|TestAnUpdateFromTheRelease.*|TestAFreshInstallFindsItsHost.*|TestAfterAnUpdateAReboot.*|TestAHostThatCannotRun.*|TestAMachineWithOnlyAttn.*|TestTurningTheSettingsOffAndOn.*|TestDirectLaunchStartsAgents.*)$' -count=1 -timeout=180s -v "$@"
