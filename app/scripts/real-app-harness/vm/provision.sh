#!/usr/bin/env bash
set -euo pipefail
mode="${1:?fixtures or runner}"
root="$(cd "$(dirname "$0")/../../../.." && pwd)"
export PATH="$HOME/.local/bin:$HOME/.bun/bin:$HOME/.cargo/bin:/usr/local/go/bin:$PATH"
if [[ "$(uname -s)" != Linux ]]; then
  echo 'Provisioning requires a Linux guest' >&2
  exit 1
fi
if [[ "$mode" == runner ]]; then
  . /etc/os-release
  if [[ "$ID" != ubuntu || "$VERSION_ID" != 24.04 ]]; then
    echo 'The runner provisioner requires Ubuntu 24.04; use run on other preconfigured Linux guests' >&2
    exit 1
  fi
fi
sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git curl ca-certificates unzip python3 iproute2 nodejs npm
bun_version="$(awk '$1 == "bun" {print $2}' "$root/.tool-versions")"
if [[ "$(bun --version 2>/dev/null || true)" != "$bun_version" ]]; then
  curl -fsSL https://bun.sh/install | bash -s -- "bun-v$bun_version"
fi
fixture="$HOME/.attn/real-app-harness/agent-fixtures/bin"
mkdir -p "$fixture"
install -m 0755 "$root/app/scripts/real-app-harness/mockAgent.mjs" "$fixture/attn-harness-mock-agent"
for name in claude codex copilot pi; do
  install -m 0755 "$root/app/scripts/real-app-harness/remote-agent-tripwire-shim.sh" "$fixture/$name"
done
[[ "$mode" == runner ]] || exit 0

sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq software-properties-common
sudo add-apt-repository -y ppa:fish-shell/release-4
sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq build-essential pkg-config bash coreutils desktop-file-utils fish \
  gh imagemagick libayatana-appindicator3-dev librsvg2-dev libssl-dev libwebkit2gtk-4.1-dev \
  libxdo-dev sqlite3 xauth xclip xdg-utils xdotool xvfb zsh dbus-x11
bash "$root/scripts/setup-linux-sandbox.sh"
case "$(uname -m)" in
  aarch64) go_arch=arm64; node_arch=arm64 ;;
  x86_64) go_arch=amd64; node_arch=x64 ;;
  *) echo 'Unsupported Linux architecture' >&2; exit 1 ;;
esac
go_version="$(awk '$1 == "go" {print $2}' "$root/go.mod")"
tools="$HOME/.local/share/attn-linux-tools"
mkdir -p "$tools" "$HOME/.local/bin"
if [[ ! -x "$tools/go-$go_version/bin/go" ]]; then
  temp="$(mktemp -d)"
  curl -fsSL "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz" -o "$temp/go.tgz"
  go_sha="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' | python3 -c '
import json, sys
filename = sys.argv[1]
print(next(f["sha256"] for v in json.load(sys.stdin) for f in v["files"] if f["filename"] == filename))
' "go${go_version}.linux-${go_arch}.tar.gz")"
  (cd "$temp" && printf '%s  go.tgz\n' "$go_sha" | sha256sum -c)
  tar -xzf "$temp/go.tgz" -C "$temp"
  mv "$temp/go" "$tools/go-$go_version"
  rm -rf "$temp"
fi
ln -sfn "$tools/go-$go_version/bin/go" "$HOME/.local/bin/go"
ln -sfn "$tools/go-$go_version/bin/gofmt" "$HOME/.local/bin/gofmt"
node_version="v22.22.0"
if [[ ! -d "$tools/node-$node_version" ]]; then
  temp="$(mktemp -d)"
  archive="node-$node_version-linux-$node_arch.tar.xz"
  curl -fsSL "https://nodejs.org/dist/$node_version/$archive" -o "$temp/$archive"
  curl -fsSL "https://nodejs.org/dist/$node_version/SHASUMS256.txt" -o "$temp/SHASUMS256.txt"
  (cd "$temp" && awk -v file="$archive" '$2 == file' SHASUMS256.txt | sha256sum -c)
  tar -xJf "$temp/$archive" -C "$temp"
  mv "$temp/node-$node_version-linux-$node_arch" "$tools/node-$node_version"
  rm -rf "$temp"
fi
for tool in node npm npx corepack; do
  ln -sfn "$tools/node-$node_version/bin/$tool" "$HOME/.local/bin/$tool"
done
npm install --global --prefix "$HOME/.local" pnpm@9
if ! command -v rustup >/dev/null; then
  curl --proto '=https' --tlsv1.2 -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain none
fi
rust_version="$(awk -F '"' '/^channel =/ {print $2}' "$root/rust-toolchain.toml")"
rustup toolchain install "$rust_version" --profile minimal
rustup default "$rust_version"
printf '\nRunner tools:\n'
go version
node --version
pnpm --version
bun --version
rustc --version
