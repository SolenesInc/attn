#!/usr/bin/env bash
#
# Build the packaged attn .app for a single profile, deriving ALL bundle
# metadata (productName, bundle id, ws port, deep-link scheme) from the single
# authority: `attn profile resolve`. The default/prod profile builds from the
# committed tauri.conf.json; every NAMED profile gets a generated, gitignored
# `tauri.<appName>.gen.conf.json` overlay (from `attn profile tauri-config`) so
# no per-profile bundle metadata is ever hand-maintained.
#
# Inputs (env):
#   PROFILE                  profile name ("" = default/prod)
#   ATTN_BIN                 path to the freshly built attn binary (the authority + sidecar)
#   VERSION SOURCE_FINGERPRINT GIT_COMMIT BUILD_TIME   build-identity stamp
#   SOURCE_DIRTY_PATHS_BASE64  NUL-separated relevant dirty paths, base64 encoded
#   MACOS_CODESIGN_IDENTITY  optional; else discovered, else ad-hoc ("-")
#   ATTN_APP_CARGO_PROFILE   cargo profile for a named profile (default: fast);
#                            the default/prod bundle always builds with release
#
# Output lands at app/src-tauri/target/staged/<appName>.app (macOS) or
# app/src-tauri/target/staged/linux-tree/<appName>, whatever cargo profile built it.
#
# Not using `set -u`: macOS ships bash 3.2 where empty-array/var expansion under
# `set -u` is fragile. Required vars are validated explicitly.
set -eo pipefail

profile="${PROFILE:-}"
harness_default="${ATTN_BUILD_DEFAULT_PROFILE_HARNESS:-}"
attn="${ATTN_BIN:?ATTN_BIN (path to built attn binary) is required}"
pty_host="${ATTN_PTY_HOST_BIN:?ATTN_PTY_HOST_BIN is required}"
: "${VERSION:?VERSION is required}"
: "${SOURCE_FINGERPRINT:?SOURCE_FINGERPRINT is required}"
: "${GIT_COMMIT:?GIT_COMMIT is required}"
: "${BUILD_TIME:?BUILD_TIME is required}"
SOURCE_DIRTY_PATHS_BASE64="${SOURCE_DIRTY_PATHS_BASE64:-}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Resolve every resource from the one authority.
app_name="$("$attn" profile resolve --profile "$profile" --field appName)"
bundle_id="$("$attn" profile resolve --profile "$profile" --field bundleId)"
ws_port="$("$attn" profile resolve --profile "$profile" --field wsPort)"
label="$("$attn" profile resolve --profile "$profile" --field label)"

if [ -n "$harness_default" ] && [ -z "$profile" ]; then
  echo "ATTN_BUILD_DEFAULT_PROFILE_HARNESS requires a named packaging profile" >&2
  exit 1
fi

echo ">>> Building $label app: $app_name (id=$bundle_id, port=$ws_port)"

if [ "$(uname -s)" = "Darwin" ]; then
  bundle_args="--bundles app"
else
  bundle_args="--no-bundle"
fi

# `fast` is `release` with app-core unoptimized (app/src-tauri/Cargo.toml): a
# Rust change costs a quarter of the CPU. What ships is always `release`.
if [ -n "$profile" ]; then
  cargo_profile="${ATTN_APP_CARGO_PROFILE:-fast}"
else
  cargo_profile="release"
fi
echo ">>> Cargo profile: $cargo_profile"

host_triple="$(rustc -vV | awk '/host:/ {print $2}')"
if [ -z "$host_triple" ]; then
  echo "could not read the rustc host triple (rustc -vV)" >&2
  exit 1
fi
mkdir -p app/src-tauri/binaries
cp "$attn" "app/src-tauri/binaries/attn-${host_triple}"
cp "$pty_host" "app/src-tauri/binaries/attn-pty-host-${host_triple}"

# Compile first-party plugins before Tauri collects resources. Bundled means
# available in the catalog; the daemon still requires a per-profile opt-in.
bash ./scripts/build-bundled-plugins.sh

# The shared app runtime, compiled the same way and for the same reason: a
# GUI-spawned daemon cannot resolve bun on a developer PATH.
bash ./scripts/build-app-runtime-host.sh

cd app
pnpm install

if [ -n "$profile" ]; then
  # Named profile: generate the bundle-metadata overlay from the authority and
  # bake the resolved port + bundle id so the Rust runtime view can never drift.
  gen_rel="src-tauri/${app_name}.gen.conf.json"
  "$attn" profile tauri-config --profile "$profile" --base src-tauri/tauri.conf.json > "$gen_rel"
  echo ">>> Generated Tauri overlay $gen_rel"
  if [ -n "$harness_default" ]; then
    echo ">>> Logical runtime profile: default, isolated by ATTN_HARNESS_DATA_DIR"
    ATTN_BUILD_PROFILE="" \
    VITE_ATTN_BUILD_PROFILE="" \
    ATTN_BUILD_WS_PORT="$ws_port" \
    ATTN_BUILD_BUNDLE_ID="$bundle_id" \
    ATTN_BUILD_DEFAULT_PROFILE_HARNESS=1 \
    VITE_DAEMON_PORT="$ws_port" \
    VITE_INSTALL_CHANNEL=source \
    VITE_ATTN_BUILD_VERSION="$VERSION" \
    VITE_ATTN_SOURCE_FINGERPRINT="$SOURCE_FINGERPRINT" \
    VITE_ATTN_SOURCE_DIRTY_PATHS_BASE64="$SOURCE_DIRTY_PATHS_BASE64" \
    VITE_ATTN_GIT_COMMIT="$GIT_COMMIT" \
    VITE_ATTN_BUILD_TIME="$BUILD_TIME" \
    pnpm tauri build $bundle_args --config "$gen_rel" -- --profile "$cargo_profile"
  else
    ATTN_BUILD_PROFILE="$profile" \
    VITE_ATTN_BUILD_PROFILE="$profile" \
    ATTN_BUILD_WS_PORT="$ws_port" \
    ATTN_BUILD_BUNDLE_ID="$bundle_id" \
    VITE_DAEMON_PORT="$ws_port" \
    VITE_INSTALL_CHANNEL=source \
    VITE_ATTN_BUILD_VERSION="$VERSION" \
    VITE_ATTN_SOURCE_FINGERPRINT="$SOURCE_FINGERPRINT" \
    VITE_ATTN_SOURCE_DIRTY_PATHS_BASE64="$SOURCE_DIRTY_PATHS_BASE64" \
    VITE_ATTN_GIT_COMMIT="$GIT_COMMIT" \
    VITE_ATTN_BUILD_TIME="$BUILD_TIME" \
    pnpm tauri build $bundle_args --config "$gen_rel" -- --profile "$cargo_profile"
  fi
else
  # Default/prod build: committed tauri.conf.json, no baked profile env. This is
  # byte-for-byte the historical `make build-app` command.
  VITE_INSTALL_CHANNEL=source \
  VITE_ATTN_BUILD_VERSION="$VERSION" \
  VITE_ATTN_SOURCE_FINGERPRINT="$SOURCE_FINGERPRINT" \
  VITE_ATTN_SOURCE_DIRTY_PATHS_BASE64="$SOURCE_DIRTY_PATHS_BASE64" \
  VITE_ATTN_GIT_COMMIT="$GIT_COMMIT" \
  VITE_ATTN_BUILD_TIME="$BUILD_TIME" \
  pnpm tauri build $bundle_args -- --profile "$cargo_profile"
fi
cd "$repo_root"

target_dir="${CARGO_TARGET_DIR:-$repo_root/app/src-tauri/target}"
staged_dir="app/src-tauri/target/staged"
bundle_dir="${staged_dir}/${app_name}.app"

write_build_identity() {
  printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s"\n}\n' \
    "$VERSION" "$SOURCE_FINGERPRINT" "$GIT_COMMIT" "$BUILD_TIME" > "$1"
}

if [ "$(uname -s)" != "Darwin" ]; then
  tree_dir="${staged_dir}/linux-tree/${app_name}"
  rm -rf "$tree_dir"
  mkdir -p "${tree_dir}/bin" "${tree_dir}/resources"
  cp "${target_dir}/${cargo_profile}/app" "${tree_dir}/bin/attn-app"
  cp "$attn" "${tree_dir}/bin/attn"
  cp "$pty_host" "${tree_dir}/bin/attn-pty-host"
  cp -R app/src-tauri/bundled-plugins "${tree_dir}/resources/plugins"
  cp -R app/src-tauri/app-runtime "${tree_dir}/resources/app-runtime"
  write_build_identity "${tree_dir}/resources/build-identity.json"
  echo ">>> Built $tree_dir"
  exit 0
fi

if [ "$(uname -s)" = "Darwin" ]; then
  mkdir -p "$staged_dir"
  rm -rf "$bundle_dir"
  mv "${target_dir}/${cargo_profile}/bundle/macos/${app_name}.app" "$bundle_dir"
  mkdir -p "${bundle_dir}/Contents/Resources"
  write_build_identity "${bundle_dir}/Contents/Resources/build-identity.json"

  # Sign the sidecar first, then the enclosing app bundle, so macOS privacy
  # grants attach to a stable signed identity across source rebuilds. One
  # machine-wide identity signs every profile's bundle (grants are keyed by
  # bundle id, so each profile grants once on first launch).
  identity="${MACOS_CODESIGN_IDENTITY:-}"
  if [ -z "$identity" ]; then identity="$(bash ./scripts/macos-codesign-identity.sh find)"; fi
  if [ -z "$identity" ]; then identity="-"; fi
  while IFS= read -r executable; do
    codesign --force --sign "$identity" "$executable"
  done < <(find "${bundle_dir}/Contents/Resources/plugins" "${bundle_dir}/Contents/Resources/app-runtime" -type f -perm -111 2>/dev/null | sort)
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn"
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn-pty-host"
  codesign --force --sign "$identity" "${bundle_dir}"
fi

echo ">>> Built $bundle_dir"
