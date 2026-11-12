#!/usr/bin/env bash
#
# Build the packaged attn .app for a single instance. Bundle metadata
# (productName, bundle id, deep-link scheme) comes from `attn instance resolve`,
# and the ws port from `attn instance claim-port`, which keeps it in the
# instance's data dir. The default/prod instance builds from the
# committed tauri.conf.json; every NAMED instance gets a generated, gitignored
# `tauri.<appName>.gen.conf.json` overlay (from `attn instance tauri-config`) so
# no per-instance bundle metadata is ever hand-maintained.
#
# APP=native builds the native app (native/crates/attn-app) instead, into
# native/target/staged, with the same identity baked in and the same sidecars.
#
# Inputs (env):
#   INSTANCE                  instance name ("" = default/prod)
#   APP                       tauri (default) or native
#   ATTN_BIN                 path to the freshly built attn binary (the authority + sidecar)
#   ATTND_BIN                path to the freshly built attnd binary (sidecar)
#   VERSION SOURCE_FINGERPRINT GIT_COMMIT BUILD_TIME   build-identity stamp
#   SOURCE_DIRTY_PATHS_BASE64  NUL-separated relevant dirty paths, base64 encoded
#   MACOS_CODESIGN_IDENTITY  optional; else discovered, else ad-hoc ("-")
#   ATTN_BUILD_DEMO          optional; 1 builds a named instance for recordings
#   ATTN_APP_CARGO_PROFILE   cargo profile for a named instance (default: fast);
#                            the default/prod bundle always builds with release
#
# Output lands at app/src-tauri/target/staged/<appName>.app (macOS) or
# app/src-tauri/target/staged/linux-tree/<appName>, whatever cargo profile built it.
#
# Not using `set -u`: macOS ships bash 3.2 where empty-array/var expansion under
# `set -u` is fragile. Required vars are validated explicitly.
set -eo pipefail

instance="${INSTANCE:-}"
harness_default="${ATTN_BUILD_DEFAULT_INSTANCE_HARNESS:-}"
demo="${ATTN_BUILD_DEMO:-}"
if [ -n "$demo" ] && [ -z "$instance" ]; then
  echo "DEMO=1 needs a named instance (make install INSTANCE=demo DEMO=1)" >&2
  exit 1
fi
attn="${ATTN_BIN:?ATTN_BIN (path to built attn binary) is required}"
attnd="${ATTND_BIN:?ATTND_BIN (path to built attnd binary) is required}"
pty_host="${ATTN_PTY_HOST_BIN:?ATTN_PTY_HOST_BIN is required}"
: "${VERSION:?VERSION is required}"
: "${SOURCE_FINGERPRINT:?SOURCE_FINGERPRINT is required}"
: "${GIT_COMMIT:?GIT_COMMIT is required}"
: "${BUILD_TIME:?BUILD_TIME is required}"
SOURCE_DIRTY_PATHS_BASE64="${SOURCE_DIRTY_PATHS_BASE64:-}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Resolve every resource from the one authority.
app_name="$("$attn" instance resolve --instance "$instance" --field appName)"
bundle_id="$("$attn" instance resolve --instance "$instance" --field bundleId)"
ws_port="$("$attn" instance claim-port --instance "$instance")"
label="$("$attn" instance resolve --instance "$instance" --field label)"

if [ -n "$harness_default" ] && [ -z "$instance" ]; then
  echo "ATTN_BUILD_DEFAULT_INSTANCE_HARNESS requires a named packaging instance" >&2
  exit 1
fi

app_shell="${APP:-tauri}"
case "$app_shell" in
  tauri | native) ;;
  *)
    echo "APP must be tauri or native, got: $app_shell" >&2
    exit 1
    ;;
esac
if [ "$app_shell" = native ] && { [ -n "$demo" ] || [ -n "$harness_default" ]; }; then
  echo "APP=native does not build DEMO=1 or default-instance harness apps yet" >&2
  exit 1
fi

echo ">>> Building $label $app_shell app: $app_name (id=$bundle_id, port=$ws_port)"

write_build_identity() {
  printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s",\n  "demo": %s,\n  "app": "%s"\n}\n' \
    "$VERSION" "$SOURCE_FINGERPRINT" "$GIT_COMMIT" "$BUILD_TIME" "$([ -n "$demo" ] && echo true || echo false)" "$app_shell" > "$1"
}

codesign_identity() {
  local identity="${MACOS_CODESIGN_IDENTITY:-}"
  if [ -z "$identity" ]; then identity="$(bash ./scripts/macos-codesign-identity.sh find)"; fi
  if [ -z "$identity" ]; then identity="-"; fi
  printf '%s' "$identity"
}

if [ "$app_shell" = native ]; then
  if [ -n "$instance" ]; then
    native_profile="${ATTN_APP_CARGO_PROFILE:-fast}"
  else
    native_profile="release"
  fi
  echo ">>> Cargo profile: $native_profile"
  (
    cd native
    ATTN_BUILD_INSTANCE="$instance" \
    ATTN_BUILD_WS_PORT="$ws_port" \
    ATTN_BUILD_BUNDLE_ID="$bundle_id" \
      cargo build --locked --profile "$native_profile" -p attn-app
  )
  native_profile_dir="$native_profile"
  [ "$native_profile" = dev ] && native_profile_dir=debug
  native_bin="${CARGO_TARGET_DIR:-native/target}/${native_profile_dir}/attn-app"
  staged_dir="native/target/staged"
  if [ "$(uname -s)" != "Darwin" ]; then
    tree_dir="${staged_dir}/linux-tree/${app_name}"
    rm -rf "$tree_dir"
    mkdir -p "${tree_dir}/bin" "${tree_dir}/resources"
    cp "$native_bin" "${tree_dir}/bin/attn-app"
    cp "$attnd" "${tree_dir}/bin/attnd"
    cp "$attn" "${tree_dir}/bin/attn"
    cp "$pty_host" "${tree_dir}/bin/attn-pty-host"
    write_build_identity "${tree_dir}/resources/build-identity.json"
    echo ">>> Built $tree_dir"
    exit 0
  fi
  bundle_dir="${staged_dir}/${app_name}.app"
  rm -rf "$bundle_dir"
  mkdir -p "${bundle_dir}/Contents/MacOS" "${bundle_dir}/Contents/Resources"
  cp "$native_bin" "${bundle_dir}/Contents/MacOS/attn-app"
  cp "$attnd" "${bundle_dir}/Contents/MacOS/attnd"
  cp "$attn" "${bundle_dir}/Contents/MacOS/attn"
  cp "$pty_host" "${bundle_dir}/Contents/MacOS/attn-pty-host"
  cp app/src-tauri/icons/icon.icns "${bundle_dir}/Contents/Resources/icon.icns"
  write_build_identity "${bundle_dir}/Contents/Resources/build-identity.json"
  cat > "${bundle_dir}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>attn-app</string>
  <key>CFBundleIdentifier</key><string>${bundle_id}</string>
  <key>CFBundleName</key><string>${app_name}</string>
  <key>CFBundleDisplayName</key><string>${app_name}</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key><string>Microphone access lets hosted coding agents receive voice input.</string>
</dict>
</plist>
PLIST
  identity="$(codesign_identity)"
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attnd"
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn"
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn-pty-host"
  codesign --force --sign "$identity" --entitlements app/src-tauri/Entitlements.plist "${bundle_dir}"
  echo ">>> Built $bundle_dir"
  exit 0
fi

if [ "$(uname -s)" = "Darwin" ]; then
  bundle_args="--bundles app"
else
  bundle_args="--no-bundle"
fi

# `fast` is `release` with app-core unoptimized (app/src-tauri/Cargo.toml): a
# Rust change costs a quarter of the CPU. What ships is always `release`.
if [ -n "$instance" ]; then
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
cp "$attnd" "app/src-tauri/binaries/attnd-${host_triple}"
cp "$attn" "app/src-tauri/binaries/attn-${host_triple}"
cp "$pty_host" "app/src-tauri/binaries/attn-pty-host-${host_triple}"

cd app
pnpm install

if [ -n "$instance" ]; then
  # Named instance: generate the bundle-metadata overlay from the authority and
  # bake the resolved port + bundle id so the Rust runtime view can never drift.
  gen_rel="src-tauri/${app_name}.gen.conf.json"
  "$attn" instance tauri-config --instance "$instance" ${demo:+--demo} --base src-tauri/tauri.conf.json > "$gen_rel"
  echo ">>> Generated Tauri overlay $gen_rel"
  if [ -n "$harness_default" ]; then
    echo ">>> Logical runtime instance: default, isolated by ATTN_HARNESS_DATA_DIR"
    ATTN_BUILD_INSTANCE="" \
    VITE_ATTN_BUILD_INSTANCE="" \
    ATTN_BUILD_WS_PORT="$ws_port" \
    ATTN_BUILD_BUNDLE_ID="$bundle_id" \
    ATTN_BUILD_DEFAULT_INSTANCE_HARNESS=1 \
    VITE_DAEMON_PORT="$ws_port" \
    VITE_INSTALL_CHANNEL=source \
    VITE_ATTN_BUILD_VERSION="$VERSION" \
    VITE_ATTN_SOURCE_FINGERPRINT="$SOURCE_FINGERPRINT" \
    VITE_ATTN_SOURCE_DIRTY_PATHS_BASE64="$SOURCE_DIRTY_PATHS_BASE64" \
    VITE_ATTN_GIT_COMMIT="$GIT_COMMIT" \
    VITE_ATTN_BUILD_TIME="$BUILD_TIME" \
    pnpm tauri build $bundle_args --config "$gen_rel" -- --profile "$cargo_profile"
  else
    ATTN_BUILD_INSTANCE="$instance" \
    VITE_ATTN_BUILD_INSTANCE="$instance" \
    VITE_ATTN_BUILD_DEMO="$demo" \
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
  # Default/prod build: committed tauri.conf.json, no baked instance env. This is
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

if [ "$(uname -s)" != "Darwin" ]; then
  tree_dir="${staged_dir}/linux-tree/${app_name}"
  rm -rf "$tree_dir"
  mkdir -p "${tree_dir}/bin" "${tree_dir}/resources"
  cp "${target_dir}/${cargo_profile}/app" "${tree_dir}/bin/attn-app"
  cp "$attnd" "${tree_dir}/bin/attnd"
  cp "$attn" "${tree_dir}/bin/attn"
  cp "$pty_host" "${tree_dir}/bin/attn-pty-host"
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
  # machine-wide identity signs every instance's bundle (grants are keyed by
  # bundle id, so each instance grants once on first launch).
  identity="$(codesign_identity)"
  while IFS= read -r executable; do
    codesign --force --sign "$identity" "$executable"
  done < <(find "${bundle_dir}/Contents/Resources/plugins" -type f -perm -111 2>/dev/null | sort)
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attnd"
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn"
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn-pty-host"
  codesign --force --sign "$identity" "${bundle_dir}"
fi

echo ">>> Built $bundle_dir"
