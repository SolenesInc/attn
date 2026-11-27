#!/usr/bin/env bash
#
# Build the packaged attn app (native/crates/attn-app) for a single instance.
# Bundle metadata (productName, bundle id, deep-link scheme) comes from
# `attn instance resolve`, and the ws port from `attn instance claim-port`,
# which keeps it in the instance's data dir and in the app's build-identity.json
# for a copy of the app installed on another machine. The app's identity names its own
# source (`scripts/source-fingerprint.sh --app native`), which covers native/;
# the sidecars keep the SOURCE_FINGERPRINT they were built with.
#
# Inputs (env):
#   INSTANCE                  instance name ("" = default/prod)
#   ATTN_BIN                 path to the freshly built attn binary (the authority + sidecar)
#   ATTND_BIN                path to the freshly built attnd binary (sidecar)
#   ATTN_PTY_HOST_BIN        path to the freshly built attn-pty-host binary (sidecar)
#   VERSION GIT_COMMIT BUILD_TIME   build-identity stamp
#   MACOS_CODESIGN_IDENTITY  optional; else discovered, else ad-hoc ("-")
#   ATTN_APP_CARGO_PROFILE   cargo profile for a named instance (default: fast);
#                            the default/prod bundle always builds with release
#
# Output lands at native/target/staged/<appName>.app (macOS) or
# native/target/staged/linux-tree/<appName>, whatever cargo profile built it.
#
# Not using `set -u`: macOS ships bash 3.2 where empty-array/var expansion under
# `set -u` is fragile. Required vars are validated explicitly.
set -eo pipefail

instance="${INSTANCE:-}"
attn="${ATTN_BIN:?ATTN_BIN (path to built attn binary) is required}"
attnd="${ATTND_BIN:?ATTND_BIN (path to built attnd binary) is required}"
pty_host="${ATTN_PTY_HOST_BIN:?ATTN_PTY_HOST_BIN is required}"
: "${VERSION:?VERSION is required}"
: "${GIT_COMMIT:?GIT_COMMIT is required}"
: "${BUILD_TIME:?BUILD_TIME is required}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

app_name="$("$attn" instance resolve --instance "$instance" --field appName)"
bundle_id="$("$attn" instance resolve --instance "$instance" --field bundleId)"
ws_port="$("$attn" instance claim-port --instance "$instance")"
label="$("$attn" instance resolve --instance "$instance" --field label)"
deep_link_scheme="$("$attn" instance resolve --instance "$instance" --field deepLinkScheme)"

echo ">>> Building $label app: $app_name (id=$bundle_id, port=$ws_port)"

source_fingerprint="$(bash ./scripts/source-fingerprint.sh --app native --field fingerprint)"
source_dirty_paths_base64="$(bash ./scripts/source-fingerprint.sh --app native --field dirty_paths_base64)"

write_build_identity() {
  printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s",\n  "wsPort": "%s"\n}\n' \
    "$VERSION" "$source_fingerprint" "$GIT_COMMIT" "$BUILD_TIME" "$ws_port" > "$1"
}

codesign_identity() {
  local identity="${MACOS_CODESIGN_IDENTITY:-}"
  if [ -z "$identity" ]; then identity="$(bash ./scripts/macos-codesign-identity.sh find)"; fi
  if [ -z "$identity" ]; then identity="-"; fi
  printf '%s' "$identity"
}

if [ -n "$instance" ]; then
  cargo_profile="${ATTN_APP_CARGO_PROFILE:-fast}"
else
  cargo_profile="release"
fi
echo ">>> Cargo profile: $cargo_profile"
(
  cd native
  ATTN_BUILD_INSTANCE="$instance" \
  ATTN_BUILD_WS_PORT="$ws_port" \
  ATTN_BUILD_BUNDLE_ID="$bundle_id" \
  ATTN_BUILD_VERSION="$VERSION" \
  ATTN_BUILD_SOURCE_FINGERPRINT="$source_fingerprint" \
  ATTN_BUILD_SOURCE_DIRTY_PATHS_BASE64="$source_dirty_paths_base64" \
  ATTN_BUILD_GIT_COMMIT="$GIT_COMMIT" \
  ATTN_BUILD_TIME="$BUILD_TIME" \
    cargo build --locked --profile "$cargo_profile" -p attn-app
)
cargo_profile_dir="$cargo_profile"
[ "$cargo_profile" = dev ] && cargo_profile_dir=debug
app_bin="${CARGO_TARGET_DIR:-native/target}/${cargo_profile_dir}/attn-app"
staged_dir="native/target/staged"

if [ "$(uname -s)" != "Darwin" ]; then
  tree_dir="${staged_dir}/linux-tree/${app_name}"
  rm -rf "$tree_dir"
  mkdir -p "${tree_dir}/bin" "${tree_dir}/resources"
  cp "$app_bin" "${tree_dir}/bin/attn-app"
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
cp "$app_bin" "${bundle_dir}/Contents/MacOS/attn-app"
cp "$attnd" "${bundle_dir}/Contents/MacOS/attnd"
cp "$attn" "${bundle_dir}/Contents/MacOS/attn"
cp "$pty_host" "${bundle_dir}/Contents/MacOS/attn-pty-host"
cp native/crates/attn-app/macos/icon.icns "${bundle_dir}/Contents/Resources/icon.icns"
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
  <key>CFBundleURLTypes</key>
  <array>
    <dict>
      <key>CFBundleURLName</key><string>${bundle_id}</string>
      <key>CFBundleURLSchemes</key><array><string>${deep_link_scheme}</string></array>
    </dict>
  </array>
  <key>NSMicrophoneUsageDescription</key><string>Microphone access lets hosted coding agents receive voice input.</string>
</dict>
</plist>
PLIST
identity="$(codesign_identity)"
sign_args=(--force --options runtime --sign "$identity")
if [ "$identity" != - ] && security find-identity -v -p codesigning | grep -F "$identity" | grep -q '"Developer ID Application:'; then
  sign_args+=(--timestamp)
fi
codesign "${sign_args[@]}" "${bundle_dir}/Contents/MacOS/attnd"
codesign "${sign_args[@]}" "${bundle_dir}/Contents/MacOS/attn"
codesign "${sign_args[@]}" "${bundle_dir}/Contents/MacOS/attn-pty-host"
codesign "${sign_args[@]}" --entitlements native/crates/attn-app/macos/Entitlements.plist "${bundle_dir}"
echo ">>> Built $bundle_dir"
