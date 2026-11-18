#!/usr/bin/env bash
#
# Check that a built attn.app carries what users' installs depend on, whichever
# app shell built it: the bundle id and URL scheme attn resolves for the
# instance, so grants and links carry over; the version; the bundled binaries,
# none of them the main executable named attn; the hardened runtime on each, which
# notarization requires; the microphone entitlement and its usage text; and a
# valid signature.
#
# usage: scripts/check-macos-release-app.sh APP VERSION
#
# INSTANCE names the instance the app was built for; empty is the default.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 APP VERSION" >&2
  exit 2
fi
app="${1%/}"
version="$2"
instance="${INSTANCE:-}"
macos="$app/Contents/MacOS"
failures=0

fail() {
  echo "release app check: $*" >&2
  failures=$((failures + 1))
}

plist() {
  /usr/libexec/PlistBuddy -c "Print :$1" "$app/Contents/Info.plist" 2>/dev/null || true
}

expect() {
  local what="$1" got="$2" want="$3"
  if [[ "$got" != "$want" ]]; then
    fail "$what is '$got', want '$want'"
  fi
}

want_id="$("$macos/attn" instance resolve --instance "$instance" --field bundleId)"
want_scheme="$("$macos/attn" instance resolve --instance "$instance" --field deepLinkScheme)"
expect CFBundleIdentifier "$(plist CFBundleIdentifier)" "$want_id"
expect "the URL scheme" "$(plist CFBundleURLTypes:0:CFBundleURLSchemes:0)" "$want_scheme"
expect CFBundleShortVersionString "$(plist CFBundleShortVersionString)" "$version"
expect "attnd --version" "$("$macos/attnd" --version)" "$version"
if [[ -z "$(plist NSMicrophoneUsageDescription)" ]]; then
  fail "Info.plist has no NSMicrophoneUsageDescription"
fi

executable="$(plist CFBundleExecutable)"
if [[ "$executable" == attn ]]; then
  fail "the main executable is named attn, which is the CLI's name"
fi
for binary in "$executable" attnd attn attn-pty-host; do
  if [[ ! -x "$macos/$binary" ]]; then
    fail "$macos/$binary is missing or not executable"
    continue
  fi
  signature="$(codesign -dv "$macos/$binary" 2>&1)"
  flags="${signature#*flags=*(}"
  flags="${flags%%)*}"
  if [[ ",$flags," != *,runtime,* ]]; then
    fail "$binary is not signed with the hardened runtime"
  fi
done

entitlements="$(mktemp)"
trap 'rm -f "$entitlements"' EXIT
codesign -d --entitlements - --xml "$app" >"$entitlements" 2>/dev/null || true
if [[ "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.security.device.audio-input' "$entitlements" 2>/dev/null)" != true ]]; then
  fail "the app lacks the com.apple.security.device.audio-input entitlement"
fi
if ! codesign --verify --deep --strict "$app"; then
  fail "the signature does not verify"
fi

if [[ "$failures" -gt 0 ]]; then
  exit 1
fi
echo "release app check: $app is $want_id $version, scheme $want_scheme, executable $executable"
