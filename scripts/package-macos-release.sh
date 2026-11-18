#!/usr/bin/env bash
#
# Notarize and package a signed attn.app the way a release ships it: the app
# notarized and stapled, then the versioned DMG notarized and stapled, the
# stable DMG alias the Homebrew cask points at, and the archive `attn update`
# downloads. Both app shells go through it.
#
# usage: scripts/package-macos-release.sh APP VERSION OUT_DIR
#
# Env: APPLE_API_KEY, APPLE_API_ISSUER and NOTARY_API_KEY_PATH, the App Store
# Connect key notarytool submits with.
#
# Writes to OUT_DIR: attn_<VERSION>_aarch64.dmg, attn_aarch64.dmg and
# attn_aarch64.app.tar.gz, whose top entry is attn.app.
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 APP VERSION OUT_DIR" >&2
  exit 2
fi
app="${1%/}"
version="$2"
out="$3"
: "${APPLE_API_KEY:?APPLE_API_KEY is required}"
: "${APPLE_API_ISSUER:?APPLE_API_ISSUER is required}"
: "${NOTARY_API_KEY_PATH:?NOTARY_API_KEY_PATH is required}"
[[ -d "$app" ]] || { echo "no app bundle at $app" >&2; exit 1; }

notarize() {
  local submission="$1" result status id
  result="$(xcrun notarytool submit "$submission" \
    --key "$NOTARY_API_KEY_PATH" \
    --key-id "$APPLE_API_KEY" \
    --issuer "$APPLE_API_ISSUER" \
    --wait --output-format json)"
  status="$(jq -r '.status' <<<"$result")"
  id="$(jq -r '.id' <<<"$result")"
  if [[ "$status" != Accepted ]]; then
    xcrun notarytool log "$id" \
      --key "$NOTARY_API_KEY_PATH" \
      --key-id "$APPLE_API_KEY" \
      --issuer "$APPLE_API_ISSUER"
    echo "notarization of $(basename "$submission") is $status (submission $id); Apple's log is above" >&2
    exit 1
  fi
  echo "notarization of $(basename "$submission") accepted (submission $id)"
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$app" "$work/attn.app"

codesign --verify --deep --strict --verbose=2 "$work/attn.app"

ditto -c -k --keepParent "$work/attn.app" "$work/attn.app.zip"
notarize "$work/attn.app.zip"
xcrun stapler staple "$work/attn.app"
xcrun stapler validate "$work/attn.app"
spctl -a -t exec -vv "$work/attn.app"

mkdir -p "$out"
dmg="$out/attn_${version}_aarch64.dmg"
rm -f "$dmg"
hdiutil create -volname "attn" -srcfolder "$work/attn.app" -format UDZO "$dmg"
notarize "$dmg"
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
cp "$dmg" "$out/attn_aarch64.dmg"

tar -czf "$out/attn_aarch64.app.tar.gz" -C "$work" attn.app
echo "packaged $app as $version in $out"
