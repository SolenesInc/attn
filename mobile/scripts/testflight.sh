#!/usr/bin/env bash
set -euo pipefail

TEAM_ID=VMGP8G85TK
MOBILE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT="${MOBILE_DIR}/ios/App/App.xcodeproj"
BUILD_NUMBER="$(date -u +%Y%m%d.%H%M%S)"
OUT_DIR="${MOBILE_DIR}/ios/App/build/testflight-${BUILD_NUMBER}"

fail() {
  printf '\ntestflight: %s\n' "$*" >&2
  [[ -z "${TESTFLIGHT_FAILURE_FILE:-}" ]] || printf '%s\n' "$*" >"${TESTFLIGHT_FAILURE_FILE}"
  exit 1
}

step() {
  printf '\n== %s\n' "$*"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is missing: $2"
}

[[ "$(uname -s)" == Darwin ]] || fail "this script runs on a Mac with Xcode, not on $(uname -s)"
require_command xcodebuild "install Xcode from the App Store and open it once"
require_command pnpm "install pnpm (https://pnpm.io/installation)"

KEY_DIR="$(mktemp -d)"
trap 'rm -rf "${KEY_DIR}"' EXIT
KEY_PATH="${KEY_DIR}/AuthKey.p8"

read_secret() {
  local env_name="$1" name="$2" destination="$3" error
  if [[ -n "${!env_name:-}" ]]; then
    printf '%s\n' "${!env_name}" >"${destination}"
    unset "${env_name}"
    return
  fi
  require_command secret "install the secret CLI from shell-config, or set ${env_name}"
  if ! error="$(secret get "${name}" 2>&1 >"${destination}")" || [[ ! -s "${destination}" ]]; then
    fail "secret ${name} is missing or unreadable${error:+ (${error})}. Create the App Store Connect API key as docs/internal/phone-app-releases.md says, then store it with: secret put ${name}"
  fi
}

step "App Store Connect API key"
read_secret ASC_API_KEY apple/asc-api-key "${KEY_PATH}"
read_secret ASC_KEY_ID apple/asc-key-id "${KEY_DIR}/key-id"
read_secret ASC_ISSUER_ID apple/asc-issuer-id "${KEY_DIR}/issuer-id"
KEY_ID="$(tr -d '[:space:]' <"${KEY_DIR}/key-id")"
ISSUER_ID="$(tr -d '[:space:]' <"${KEY_DIR}/issuer-id")"
grep -q -- '-----BEGIN PRIVATE KEY-----' "${KEY_PATH}" \
  || fail "the App Store Connect API key is not a .p8 private key: store the whole AuthKey_${KEY_ID}.p8 file as the secret apple/asc-api-key, or as ASC_API_KEY in the testflight GitHub Environment"
echo "key id ${KEY_ID}"

AUTH_FLAGS=(
  -allowProvisioningUpdates
  -authenticationKeyPath "${KEY_PATH}"
  -authenticationKeyID "${KEY_ID}"
  -authenticationKeyIssuerID "${ISSUER_ID}"
)

mkdir -p "${OUT_DIR}"

run_logged() {
  local name="$1" log="${OUT_DIR}/$1.log" refusal
  shift
  if ! "$@" 2>&1 | sed -e "s/${KEY_ID}/<key id>/g" -e "s/${ISSUER_ID}/<issuer id>/g" >"${log}"; then
    printf '\n%s failed. Its errors:\n' "${name}" >&2
    grep -E '(^|: )error:|^error|\*\* .* FAILED \*\*' "${log}" | sort -u >&2 || tail -n 30 "${log}" >&2
    if grep -q 'Error Downloading App Information' "${log}"; then
      fail "App Store Connect has no app record for codes.attn.mobile, or the API key cannot see it. Create it once as docs/internal/phone-app-releases.md says (One-time setup), then run this script again. The full log is ${log}"
    fi
    if refusal="$(grep -E -m1 -A1 'Validation failed.*409|Upload limit' "${log}" | tr '\n' ' ')" && [[ -n "${refusal}" ]]; then
      fail "App Store Connect refused the upload with a 409. If its answer names the upload limit, wait a day, then upload again. Otherwise, fix what it names. The full log is ${log}. Its answer: ${refusal}"
    fi
    fail "${name} failed; the full log is ${log}"
  fi
}

step "Web app"
bash "${MOBILE_DIR}/../app/scripts/ensure-ghostty-vt-wasm.sh"
pnpm --dir "${MOBILE_DIR}" install --frozen-lockfile
pnpm --dir "${MOBILE_DIR}" run build
(cd "${MOBILE_DIR}" && pnpm exec cap sync ios)

step "Archive build ${BUILD_NUMBER}"
run_logged archive xcodebuild archive \
  -project "${PROJECT}" \
  -scheme App \
  -configuration Release \
  -destination generic/platform=iOS \
  -archivePath "${OUT_DIR}/App.xcarchive" \
  DEVELOPMENT_TEAM="${TEAM_ID}" \
  CODE_SIGN_STYLE=Manual \
  CODE_SIGN_IDENTITY=- \
  AD_HOC_CODE_SIGNING_ALLOWED=YES \
  PROVISIONING_PROFILE_SPECIFIER= \
  CURRENT_PROJECT_VERSION="${BUILD_NUMBER}"
VERSION="$(/usr/libexec/PlistBuddy -c 'Print :ApplicationProperties:CFBundleShortVersionString' "${OUT_DIR}/App.xcarchive/Info.plist")"
echo "archived attn ${VERSION} (${BUILD_NUMBER})"

step "Sign and upload to App Store Connect"
cat >"${OUT_DIR}/ExportOptions.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key>
  <string>app-store-connect</string>
  <key>destination</key>
  <string>upload</string>
  <key>teamID</key>
  <string>${TEAM_ID}</string>
  <key>signingStyle</key>
  <string>automatic</string>
  <key>manageAppVersionAndBuildNumber</key>
  <false/>
  <key>uploadSymbols</key>
  <true/>
</dict>
</plist>
PLIST
run_logged upload xcodebuild -exportArchive \
  -archivePath "${OUT_DIR}/App.xcarchive" \
  -exportOptionsPlist "${OUT_DIR}/ExportOptions.plist" \
  -exportPath "${OUT_DIR}/export" \
  "${AUTH_FLAGS[@]}"

step "Done"
echo "Uploaded attn ${VERSION} (${BUILD_NUMBER}) to App Store Connect."
echo "It appears in the TestFlight app once App Store Connect finishes processing it."
echo "Archive and logs: ${OUT_DIR}"
