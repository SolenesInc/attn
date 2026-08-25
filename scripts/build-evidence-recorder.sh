#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "The evidence recorder can only be built on macOS" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_root="${repo_root}/app/src-tauri/target/evidence-recorder"
bundle="${build_root}/AttnRecorder.app"
macos_dir="${bundle}/Contents/MacOS"
helpers_dir="${bundle}/Contents/Helpers"
module_cache="${build_root}/swift-module-cache"

rm -rf "${bundle}"
mkdir -p "${macos_dir}" "${helpers_dir}" "${module_cache}"
cp "${repo_root}/app/scripts/real-app-harness/recorder/Info.plist" "${bundle}/Contents/Info.plist"

go -C "${repo_root}" build -o "${macos_dir}/attn-recorder" ./cmd/attn-recorder
/usr/bin/swiftc -O \
  -module-cache-path "${module_cache}" \
  -Xlinker -sectcreate \
  -Xlinker __TEXT \
  -Xlinker __info_plist \
  -Xlinker "${repo_root}/app/scripts/real-app-harness/recorder/CaptureInfo.plist" \
  "${repo_root}/app/scripts/real-app-harness/WindowRecorder.swift" \
  -o "${helpers_dir}/AttnRecorderCapture"

identity="${MACOS_CODESIGN_IDENTITY:-}"
if [[ -z "${identity}" ]]; then
  identity="$(bash "${repo_root}/scripts/macos-codesign-identity.sh" find)"
fi
if [[ -z "${identity}" ]]; then identity="-"; fi
/usr/bin/codesign --force --identifier com.attn.recorder.capture --sign "${identity}" "${helpers_dir}/AttnRecorderCapture"
/usr/bin/codesign --force --sign "${identity}" "${macos_dir}/attn-recorder"
/usr/bin/codesign --force --sign "${identity}" "${bundle}"

echo "Built ${bundle}"
